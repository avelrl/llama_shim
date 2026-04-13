package retrieval

import (
	"strings"
	"unicode"
)

const maxPlannedFileSearchQueries = 4

var queryRewriteStopwords = map[string]struct{}{
	"a": {}, "about": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "been": {}, "being": {},
	"between": {}, "but": {}, "by": {}, "can": {}, "compare": {}, "compared": {}, "comparison": {},
	"could": {}, "describe": {}, "did": {}, "difference": {}, "different": {}, "do": {}, "does": {},
	"explain": {}, "for": {}, "from": {}, "give": {}, "help": {}, "how": {}, "i": {}, "in": {},
	"into": {}, "is": {}, "it": {}, "know": {}, "like": {}, "may": {}, "me": {}, "might": {},
	"must": {}, "my": {}, "need": {}, "of": {}, "on": {}, "or": {}, "our": {}, "please": {},
	"show": {}, "should": {}, "tell": {}, "that": {}, "the": {}, "their": {}, "them": {}, "these": {},
	"they": {}, "this": {}, "those": {}, "to": {}, "understand": {}, "us": {}, "versus": {},
	"vs": {}, "want": {}, "was": {}, "we": {}, "were": {}, "what": {}, "when": {}, "where": {},
	"which": {}, "who": {}, "why": {}, "will": {}, "with": {}, "would": {}, "you": {}, "your": {},
}

func RewriteSearchQueries(queries []string) []string {
	out := make([]string, 0, len(queries))
	seen := make(map[string]struct{}, len(queries))
	for _, query := range queries {
		rewritten := rewriteSearchQuery(query)
		if rewritten == "" {
			continue
		}
		if _, ok := seen[rewritten]; ok {
			continue
		}
		seen[rewritten] = struct{}{}
		out = append(out, rewritten)
	}
	return out
}

func PlanFileSearchQueries(query string) []string {
	base := strings.TrimSpace(query)
	if base == "" {
		return nil
	}

	planned := make([]string, 0, maxPlannedFileSearchQueries)
	seen := map[string]struct{}{}
	add := func(candidate string) {
		if len(planned) >= maxPlannedFileSearchQueries {
			return
		}
		rewritten := rewriteSearchQuery(candidate)
		if rewritten == "" {
			return
		}
		if _, ok := seen[rewritten]; ok {
			return
		}
		seen[rewritten] = struct{}{}
		planned = append(planned, rewritten)
	}

	add(base)
	for _, clause := range decomposeSearchClauses(base) {
		add(clause)
		if len(planned) >= maxPlannedFileSearchQueries {
			break
		}
	}

	if len(planned) == 0 {
		return []string{normalizeSearchWhitespace(base)}
	}
	return planned
}

func SearchQueryPayload(queries []string) any {
	switch len(queries) {
	case 0:
		return ""
	case 1:
		return queries[0]
	default:
		out := make([]string, len(queries))
		copy(out, queries)
		return out
	}
}

func SearchQueryPayloadLike(raw any, queries []string) any {
	switch raw.(type) {
	case []string:
		out := make([]string, len(queries))
		copy(out, queries)
		return out
	default:
		return SearchQueryPayload(queries)
	}
}

func decomposeSearchClauses(query string) []string {
	sentences := splitSearchSentences(query)
	if len(sentences) > 1 {
		return sentences
	}
	if len(sentences) == 0 {
		return nil
	}
	return splitComplexSearchClause(sentences[0])
}

func splitSearchSentences(query string) []string {
	parts := strings.FieldsFunc(query, func(r rune) bool {
		switch r {
		case '\n', '\r', '?', '!', ';':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = normalizeSearchWhitespace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func splitComplexSearchClause(query string) []string {
	trimmed := normalizeSearchWhitespace(query)
	if trimmed == "" {
		return nil
	}

	compareStyle := strings.HasPrefix(trimmed, "compare ") || strings.HasPrefix(trimmed, "difference between ")
	for _, separator := range []string{" versus ", " vs ", " compared to "} {
		if parts := splitSearchClauseBySeparator(trimmed, separator); len(parts) > 1 {
			return parts
		}
	}
	if compareStyle || countSearchTerms(trimmed) >= 8 {
		for _, separator := range []string{" and ", " or "} {
			if parts := splitSearchClauseBySeparator(trimmed, separator); len(parts) > 1 {
				return parts
			}
		}
	}
	return nil
}

func splitSearchClauseBySeparator(query, separator string) []string {
	lowered := strings.ToLower(query)
	if !strings.Contains(lowered, separator) {
		return nil
	}
	rawParts := strings.Split(lowered, separator)
	if len(rawParts) < 2 {
		return nil
	}
	out := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part = normalizeSearchWhitespace(part)
		if countSearchTerms(part) < 2 {
			return nil
		}
		out = append(out, part)
	}
	return out
}

func rewriteSearchQuery(query string) string {
	normalized := normalizeSearchWhitespace(query)
	if normalized == "" {
		return ""
	}

	tokens := tokenizeSearchTerms(normalized)
	if len(tokens) == 0 {
		return normalized
	}

	filtered := make([]string, 0, len(tokens))
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, ok := queryRewriteStopwords[token]; ok {
			continue
		}
		if len(token) == 1 && !containsDigit(token) {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		filtered = append(filtered, token)
	}
	if len(filtered) == 0 {
		return normalized
	}
	if len(filtered) > 12 {
		filtered = filtered[:12]
	}
	return strings.Join(filtered, " ")
}

func normalizeSearchWhitespace(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(strings.ToLower(text))), " ")
}

func tokenizeSearchTerms(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func countSearchTerms(text string) int {
	return len(tokenizeSearchTerms(text))
}

func containsDigit(text string) bool {
	for _, r := range text {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
