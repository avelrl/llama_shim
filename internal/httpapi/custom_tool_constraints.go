package httpapi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const customToolTransportLocalConstrained = "local_constrained"

type customToolConstraint struct {
	FormatType  string
	Syntax      string
	Definition  string
	PatternSrc  string
	Anchored    string
	Pattern     *regexp.Regexp
	VLLMGrammar string
}

func compileCustomToolConstraint(tool map[string]any, serviceLimits ServiceLimits) (*customToolConstraint, error) {
	limits := normalizeServiceLimits(serviceLimits)

	formatType := detectCustomToolFormatType(tool)
	if formatType == "" || formatType == "text" {
		return nil, nil
	}
	if formatType != "grammar" {
		return nil, fmt.Errorf("custom tool format %q is not supported by shim-local constrained tools", formatType)
	}

	format, ok := extractCustomToolFormat(tool)
	if !ok {
		return nil, fmt.Errorf("custom tool grammar metadata is missing")
	}

	syntax := strings.ToLower(strings.TrimSpace(asString(format["syntax"])))
	if syntax == "" {
		syntax = "lark"
	}
	definition := strings.TrimSpace(asString(format["definition"]))
	if definition == "" {
		return nil, fmt.Errorf("custom tool grammar definition is required")
	}
	if int64(len(definition)) > limits.CustomToolGrammarDefinitionBytes {
		return nil, fmt.Errorf(
			"custom tool grammar definition exceeds the shim-local constrained limit of %d bytes",
			limits.CustomToolGrammarDefinitionBytes,
		)
	}

	var pattern string
	var vllmGrammar string
	switch syntax {
	case "regex":
		pattern = definition
	case "lark":
		compiled, err := compileSupportedLarkToRegex(definition)
		if err != nil {
			return nil, err
		}
		pattern = compiled
		vllmCompiled, err := compileSupportedLarkToVLLMGrammar(definition)
		if err != nil {
			return nil, err
		}
		vllmGrammar = vllmCompiled
	default:
		return nil, fmt.Errorf("custom tool grammar syntax %q is not supported by shim-local constrained tools", syntax)
	}

	anchored := "^(?:" + pattern + ")$"
	if int64(len(pattern)) > limits.CustomToolCompiledPatternBytes || int64(len(anchored)) > limits.CustomToolCompiledPatternBytes {
		return nil, fmt.Errorf(
			"compiled %s grammar exceeds the shim-local constrained limit of %d bytes",
			syntax,
			limits.CustomToolCompiledPatternBytes,
		)
	}
	matcher, err := regexp.Compile(anchored)
	if err != nil {
		return nil, fmt.Errorf("compile %s grammar: %w", syntax, err)
	}
	return &customToolConstraint{
		FormatType:  formatType,
		Syntax:      syntax,
		Definition:  definition,
		PatternSrc:  pattern,
		Anchored:    anchored,
		Pattern:     matcher,
		VLLMGrammar: vllmGrammar,
	}, nil
}

func (c *customToolConstraint) Active() bool {
	return c != nil && c.Pattern != nil
}

func (c *customToolConstraint) Validate(value string) error {
	if !c.Active() {
		return nil
	}
	if c.Pattern.MatchString(value) {
		return nil
	}
	return fmt.Errorf("input does not satisfy %s %s constraint", c.FormatType, c.Syntax)
}

func (c *customToolConstraint) DescriptionHint() string {
	if c == nil {
		return ""
	}
	return "The `input` string must fully match this " + c.Syntax + " constraint: " + c.Definition
}

func extractCustomToolFormat(tool map[string]any) (map[string]any, bool) {
	if grammar, ok := tool["grammar"].(map[string]any); ok {
		out := mapsClone(grammar)
		if strings.TrimSpace(asString(out["type"])) == "" {
			out["type"] = "grammar"
		}
		return out, true
	}
	if format, ok := tool["format"].(map[string]any); ok {
		return mapsClone(format), true
	}
	return nil, false
}

type larkRegexCompiler struct {
	rules    map[string]string
	imports  map[string]string
	memo     map[string]string
	visiting map[string]bool
}

func compileSupportedLarkToRegex(definition string) (string, error) {
	compiler, err := newLarkRegexCompiler(definition)
	if err != nil {
		return "", err
	}
	return compiler.compileRule("start")
}

func compileSupportedLarkToVLLMGrammar(definition string) (string, error) {
	base, err := newLarkRegexCompiler(definition)
	if err != nil {
		return "", err
	}
	compiler := &larkVLLMGrammarCompiler{
		rules:       base.rules,
		imports:     base.imports,
		definitions: make(map[string]string),
		visiting:    make(map[string]bool),
	}
	if _, err := compiler.compileRule("start"); err != nil {
		return "", err
	}

	lines := []string{"root ::= " + compiler.definitions["start"]}
	for _, name := range compiler.order {
		if name == "start" {
			continue
		}
		lines = append(lines, name+" ::= "+compiler.definitions[name])
	}
	return strings.Join(lines, "\n"), nil
}

type larkVLLMGrammarCompiler struct {
	rules       map[string]string
	imports     map[string]string
	definitions map[string]string
	visiting    map[string]bool
	order       []string
}

func (c *larkVLLMGrammarCompiler) compileRule(name string) (string, error) {
	if _, ok := c.definitions[name]; ok {
		return name, nil
	}
	if pattern, ok := c.imports[name]; ok {
		c.addDefinition(name, pattern)
		return name, nil
	}
	if pattern, ok := supportedLarkImportPatterns[name]; ok {
		c.addDefinition(name, pattern)
		return name, nil
	}
	expr, ok := c.rules[name]
	if !ok {
		return "", fmt.Errorf("lark rule %q is not defined", name)
	}
	if c.visiting[name] {
		return "", fmt.Errorf("recursive lark rule %q is not supported by shim-local constrained tools", name)
	}
	c.visiting[name] = true
	defer delete(c.visiting, name)

	parser, err := newLarkVLLMGrammarExprParser(stripLarkAliases(expr), c)
	if err != nil {
		return "", err
	}
	compiled, err := parser.parse()
	if err != nil {
		return "", err
	}
	c.addDefinition(name, compiled)
	return name, nil
}

func (c *larkVLLMGrammarCompiler) addDefinition(name string, definition string) {
	if _, ok := c.definitions[name]; ok {
		return
	}
	c.definitions[name] = definition
	c.order = append(c.order, name)
}

func newLarkRegexCompiler(definition string) (*larkRegexCompiler, error) {
	rules := make(map[string]string)
	imports := make(map[string]string)
	lastRule := ""

	for _, rawLine := range strings.Split(definition, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "%ignore"):
			return nil, fmt.Errorf("lark %%ignore directives are not supported by shim-local constrained tools")
		case strings.HasPrefix(line, "%import"):
			importsList, err := parseSupportedLarkImport(line)
			if err != nil {
				return nil, err
			}
			for _, imported := range importsList {
				imports[imported.name] = imported.pattern
			}
		case strings.HasPrefix(line, "%declare"):
			return nil, fmt.Errorf("lark %%declare directives are not supported by shim-local constrained tools")
		case strings.HasPrefix(line, "|"):
			if lastRule == "" {
				return nil, fmt.Errorf("lark alternative is missing a parent rule")
			}
			rules[lastRule] = rules[lastRule] + " | " + strings.TrimSpace(strings.TrimPrefix(line, "|"))
		default:
			colon := strings.Index(line, ":")
			if colon <= 0 {
				return nil, fmt.Errorf("unsupported lark rule line %q", line)
			}
			name := strings.TrimSpace(line[:colon])
			if !isLarkIdentifier(name) {
				return nil, fmt.Errorf("unsupported lark rule name %q", name)
			}
			expr := strings.TrimSpace(line[colon+1:])
			if expr == "" {
				return nil, fmt.Errorf("lark rule %q is empty", name)
			}
			rules[name] = expr
			lastRule = name
		}
	}

	if _, ok := rules["start"]; !ok {
		return nil, fmt.Errorf("lark grammar must define start")
	}
	return &larkRegexCompiler{
		rules:    rules,
		imports:  imports,
		memo:     make(map[string]string),
		visiting: make(map[string]bool),
	}, nil
}

type larkImport struct {
	name    string
	pattern string
}

func parseSupportedLarkImport(line string) ([]larkImport, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "%import"))
	if trimmed == "" {
		return nil, fmt.Errorf("unsupported empty lark %%import directive")
	}

	if strings.HasPrefix(trimmed, "common.") {
		imported, err := parseSupportedCommonLarkImport(trimmed)
		if err != nil {
			return nil, err
		}
		return []larkImport{imported}, nil
	}

	if strings.HasPrefix(trimmed, "common") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "common"))
		if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
			group := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")"))
			if group == "" {
				return nil, fmt.Errorf("unsupported empty lark %%import common group")
			}
			parts := strings.Split(group, ",")
			out := make([]larkImport, 0, len(parts))
			for _, part := range parts {
				imported, err := parseSupportedCommonLarkImport(strings.TrimSpace(part))
				if err != nil {
					return nil, err
				}
				out = append(out, imported)
			}
			return out, nil
		}
	}

	return nil, fmt.Errorf("lark %%import %q is not supported by shim-local constrained tools", trimmed)
}

func parseSupportedCommonLarkImport(target string) (larkImport, error) {
	alias := ""
	if idx := strings.Index(target, "->"); idx >= 0 {
		alias = strings.TrimSpace(target[idx+2:])
		target = strings.TrimSpace(target[:idx])
	}
	base := strings.TrimSpace(strings.TrimPrefix(target, "common."))
	if strings.Contains(base, ".") || !isLarkIdentifier(base) {
		return larkImport{}, fmt.Errorf("lark %%import common.%s is not supported by shim-local constrained tools", base)
	}
	name := base
	if alias != "" {
		if !isLarkIdentifier(alias) {
			return larkImport{}, fmt.Errorf("unsupported lark %%import alias %q", alias)
		}
		name = alias
	}
	pattern, ok := supportedLarkImportPatterns[base]
	if !ok {
		return larkImport{}, fmt.Errorf("lark %%import common.%s is not supported by shim-local constrained tools", base)
	}
	return larkImport{name: name, pattern: pattern}, nil
}

var supportedLarkImportPatterns = map[string]string{
	"CNAME":          `[A-Za-z_][A-Za-z0-9_]*`,
	"CR":             `\r`,
	"CRLF":           `\r\n`,
	"DECIMAL":        `(?:[0-9]+\.[0-9]*|\.[0-9]+)`,
	"DIGIT":          `[0-9]`,
	"ESCAPED_STRING": `"(?:\\.|[^"\\])*"`,
	"FLOAT":          `(?:(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?|[0-9]+[eE][-+]?[0-9]+)`,
	"HEXDIGIT":       `[0-9A-Fa-f]`,
	"INT":            `[0-9]+`,
	"LETTER":         `[A-Za-z]`,
	"LF":             `\n`,
	"NEWLINE":        `(?:\r?\n)+`,
	"NUMBER":         `(?:[0-9]+(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?|\.[0-9]+(?:[eE][-+]?[0-9]+)?)`,
	"SIGNED_FLOAT":   `[-+]?(?:(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?|[0-9]+[eE][-+]?[0-9]+)`,
	"SIGNED_INT":     `[-+]?[0-9]+`,
	"SIGNED_NUMBER":  `[-+]?(?:[0-9]+(?:\.[0-9]*)?(?:[eE][-+]?[0-9]+)?|\.[0-9]+(?:[eE][-+]?[0-9]+)?)`,
	"WORD":           `[A-Za-z]+`,
	"WS":             `[ \t\f\r\n]+`,
	"WS_INLINE":      `[ \t]+`,
}

func (c *larkRegexCompiler) compileRule(name string) (string, error) {
	if pattern, ok := c.memo[name]; ok {
		return pattern, nil
	}
	if pattern, ok := c.imports[name]; ok {
		return pattern, nil
	}
	if pattern, ok := supportedLarkImportPatterns[name]; ok {
		return pattern, nil
	}
	expr, ok := c.rules[name]
	if !ok {
		return "", fmt.Errorf("lark rule %q is not defined", name)
	}
	if c.visiting[name] {
		return "", fmt.Errorf("recursive lark rule %q is not supported by shim-local constrained tools", name)
	}
	c.visiting[name] = true
	defer delete(c.visiting, name)

	parser, err := newLarkExprParser(stripLarkAliases(expr), c)
	if err != nil {
		return "", err
	}
	pattern, err := parser.parse()
	if err != nil {
		return "", err
	}
	c.memo[name] = pattern
	return pattern, nil
}

var larkAliasPattern = regexp.MustCompile(`\s*->\s*[A-Za-z_][A-Za-z0-9_]*`)

func stripLarkAliases(expr string) string {
	return larkAliasPattern.ReplaceAllString(expr, "")
}

type larkTokenKind int

const (
	larkTokenEOF larkTokenKind = iota
	larkTokenIdent
	larkTokenString
	larkTokenRegex
	larkTokenLParen
	larkTokenRParen
	larkTokenPipe
	larkTokenStar
	larkTokenPlus
	larkTokenQuestion
)

type larkToken struct {
	Kind larkTokenKind
	Text string
}

func tokenizeLarkExpr(expr string) ([]larkToken, error) {
	tokens := make([]larkToken, 0, len(expr))
	for i := 0; i < len(expr); {
		r := rune(expr[i])
		if unicode.IsSpace(r) {
			i++
			continue
		}
		switch expr[i] {
		case '(':
			tokens = append(tokens, larkToken{Kind: larkTokenLParen, Text: "("})
			i++
		case ')':
			tokens = append(tokens, larkToken{Kind: larkTokenRParen, Text: ")"})
			i++
		case '|':
			tokens = append(tokens, larkToken{Kind: larkTokenPipe, Text: "|"})
			i++
		case '*':
			if i+1 < len(expr) && expr[i+1] == '?' {
				return nil, fmt.Errorf("lark lazy quantifiers are not supported by shim-local constrained tools")
			}
			tokens = append(tokens, larkToken{Kind: larkTokenStar, Text: "*"})
			i++
		case '+':
			if i+1 < len(expr) && expr[i+1] == '?' {
				return nil, fmt.Errorf("lark lazy quantifiers are not supported by shim-local constrained tools")
			}
			tokens = append(tokens, larkToken{Kind: larkTokenPlus, Text: "+"})
			i++
		case '?':
			if i+1 < len(expr) && expr[i+1] == '?' {
				return nil, fmt.Errorf("lark lazy quantifiers are not supported by shim-local constrained tools")
			}
			tokens = append(tokens, larkToken{Kind: larkTokenQuestion, Text: "?"})
			i++
		case '"', '\'':
			literal, next, err := readQuotedLarkLiteral(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, larkToken{Kind: larkTokenString, Text: literal})
			i = next
		case '/':
			pattern, next, err := readLarkRegexLiteral(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, larkToken{Kind: larkTokenRegex, Text: pattern})
			i = next
		default:
			if !isLarkIdentifierStart(r) {
				return nil, fmt.Errorf("unsupported lark token %q", string(expr[i]))
			}
			start := i
			i++
			for i < len(expr) && isLarkIdentifierPart(rune(expr[i])) {
				i++
			}
			tokens = append(tokens, larkToken{Kind: larkTokenIdent, Text: expr[start:i]})
		}
	}
	tokens = append(tokens, larkToken{Kind: larkTokenEOF})
	return tokens, nil
}

func readQuotedLarkLiteral(expr string, start int) (string, int, error) {
	quote := expr[start]
	escaped := false
	for i := start + 1; i < len(expr); i++ {
		switch {
		case escaped:
			escaped = false
		case expr[i] == '\\':
			escaped = true
		case expr[i] == quote:
			return expr[start : i+1], i + 1, nil
		}
	}
	return "", 0, fmt.Errorf("unterminated lark string literal")
}

func readLarkRegexLiteral(expr string, start int) (string, int, error) {
	escaped := false
	for i := start + 1; i < len(expr); i++ {
		switch {
		case escaped:
			escaped = false
		case expr[i] == '\\':
			escaped = true
		case expr[i] == '/':
			pattern := expr[start+1 : i]
			if containsUnsupportedRegexLookaround(pattern) {
				return "", 0, fmt.Errorf("lark lexer regex lookaround is not supported by shim-local constrained tools")
			}
			if containsUnsupportedRegexLazyQuantifier(pattern) {
				return "", 0, fmt.Errorf("lark lexer regex lazy quantifiers are not supported by shim-local constrained tools")
			}
			return pattern, i + 1, nil
		}
	}
	return "", 0, fmt.Errorf("unterminated lark regex literal")
}

func containsUnsupportedRegexLookaround(pattern string) bool {
	escaped := false
	inClass := false
	for i := 0; i < len(pattern); i++ {
		switch {
		case escaped:
			escaped = false
		case pattern[i] == '\\':
			escaped = true
		case pattern[i] == '[':
			inClass = true
		case pattern[i] == ']':
			inClass = false
		case !inClass && pattern[i] == '(' && i+2 < len(pattern) && pattern[i+1] == '?':
			if pattern[i+2] == '=' || pattern[i+2] == '!' {
				return true
			}
			if pattern[i+2] == '<' && i+3 < len(pattern) && (pattern[i+3] == '=' || pattern[i+3] == '!') {
				return true
			}
		}
	}
	return false
}

func containsUnsupportedRegexLazyQuantifier(pattern string) bool {
	escaped := false
	inClass := false
	for i := 0; i+1 < len(pattern); i++ {
		switch {
		case escaped:
			escaped = false
		case pattern[i] == '\\':
			escaped = true
		case pattern[i] == '[':
			inClass = true
		case pattern[i] == ']':
			inClass = false
		case !inClass && (pattern[i] == '*' || pattern[i] == '+' || pattern[i] == '?') && pattern[i+1] == '?':
			return true
		}
	}
	return false
}

type larkExprParser struct {
	tokens   []larkToken
	position int
	compiler *larkRegexCompiler
}

func newLarkExprParser(expr string, compiler *larkRegexCompiler) (*larkExprParser, error) {
	tokens, err := tokenizeLarkExpr(expr)
	if err != nil {
		return nil, err
	}
	return &larkExprParser{tokens: tokens, compiler: compiler}, nil
}

func (p *larkExprParser) parse() (string, error) {
	expr, err := p.parseAlternation()
	if err != nil {
		return "", err
	}
	if p.peek().Kind != larkTokenEOF {
		return "", fmt.Errorf("unexpected lark token %q", p.peek().Text)
	}
	return expr, nil
}

func (p *larkExprParser) parseAlternation() (string, error) {
	first, err := p.parseSequence()
	if err != nil {
		return "", err
	}
	alternatives := []string{first}
	for p.peek().Kind == larkTokenPipe {
		p.next()
		nextExpr, err := p.parseSequence()
		if err != nil {
			return "", err
		}
		alternatives = append(alternatives, nextExpr)
	}
	if len(alternatives) == 1 {
		return alternatives[0], nil
	}
	return "(?:" + strings.Join(alternatives, "|") + ")", nil
}

func (p *larkExprParser) parseSequence() (string, error) {
	parts := make([]string, 0, 4)
	for {
		switch p.peek().Kind {
		case larkTokenEOF, larkTokenRParen, larkTokenPipe:
			if len(parts) == 0 {
				return "", fmt.Errorf("empty lark sequence is not supported by shim-local constrained tools")
			}
			return strings.Join(parts, ""), nil
		default:
			part, err := p.parseFactor()
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
	}
}

func (p *larkExprParser) parseFactor() (string, error) {
	primary, err := p.parsePrimary()
	if err != nil {
		return "", err
	}
	for {
		switch p.peek().Kind {
		case larkTokenStar:
			p.next()
			primary = "(?:" + primary + ")*"
		case larkTokenPlus:
			p.next()
			primary = "(?:" + primary + ")+"
		case larkTokenQuestion:
			p.next()
			primary = "(?:" + primary + ")?"
		default:
			return primary, nil
		}
	}
}

func (p *larkExprParser) parsePrimary() (string, error) {
	token := p.next()
	switch token.Kind {
	case larkTokenIdent:
		return p.compiler.compileRule(token.Text)
	case larkTokenString:
		literal, err := strconv.Unquote(token.Text)
		if err != nil {
			return "", fmt.Errorf("decode lark string literal: %w", err)
		}
		return regexp.QuoteMeta(literal), nil
	case larkTokenRegex:
		return token.Text, nil
	case larkTokenLParen:
		expr, err := p.parseAlternation()
		if err != nil {
			return "", err
		}
		if p.peek().Kind != larkTokenRParen {
			return "", fmt.Errorf("unclosed lark group")
		}
		p.next()
		return "(?:" + expr + ")", nil
	default:
		return "", fmt.Errorf("unexpected lark token %q", token.Text)
	}
}

func (p *larkExprParser) peek() larkToken {
	if p.position >= len(p.tokens) {
		return larkToken{Kind: larkTokenEOF}
	}
	return p.tokens[p.position]
}

func (p *larkExprParser) next() larkToken {
	token := p.peek()
	if p.position < len(p.tokens) {
		p.position++
	}
	return token
}

type larkVLLMGrammarExprParser struct {
	tokens   []larkToken
	position int
	compiler *larkVLLMGrammarCompiler
}

func newLarkVLLMGrammarExprParser(expr string, compiler *larkVLLMGrammarCompiler) (*larkVLLMGrammarExprParser, error) {
	tokens, err := tokenizeLarkExpr(expr)
	if err != nil {
		return nil, err
	}
	return &larkVLLMGrammarExprParser{tokens: tokens, compiler: compiler}, nil
}

func (p *larkVLLMGrammarExprParser) parse() (string, error) {
	expr, err := p.parseAlternation()
	if err != nil {
		return "", err
	}
	if p.peek().Kind != larkTokenEOF {
		return "", fmt.Errorf("unexpected lark token %q", p.peek().Text)
	}
	return expr, nil
}

func (p *larkVLLMGrammarExprParser) parseAlternation() (string, error) {
	first, err := p.parseSequence()
	if err != nil {
		return "", err
	}
	alternatives := []string{first}
	for p.peek().Kind == larkTokenPipe {
		p.next()
		nextExpr, err := p.parseSequence()
		if err != nil {
			return "", err
		}
		alternatives = append(alternatives, nextExpr)
	}
	return strings.Join(alternatives, " | "), nil
}

func (p *larkVLLMGrammarExprParser) parseSequence() (string, error) {
	parts := make([]string, 0, 4)
	for {
		switch p.peek().Kind {
		case larkTokenEOF, larkTokenRParen, larkTokenPipe:
			if len(parts) == 0 {
				return "", fmt.Errorf("empty lark sequence is not supported by shim-local constrained tools")
			}
			return strings.Join(parts, " "), nil
		default:
			part, err := p.parseFactor()
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
	}
}

func (p *larkVLLMGrammarExprParser) parseFactor() (string, error) {
	primary, err := p.parsePrimary()
	if err != nil {
		return "", err
	}
	for {
		switch p.peek().Kind {
		case larkTokenStar:
			p.next()
			primary += "*"
		case larkTokenPlus:
			p.next()
			primary += "+"
		case larkTokenQuestion:
			p.next()
			primary += "?"
		default:
			return primary, nil
		}
	}
}

func (p *larkVLLMGrammarExprParser) parsePrimary() (string, error) {
	token := p.next()
	switch token.Kind {
	case larkTokenIdent:
		return p.compiler.compileRule(token.Text)
	case larkTokenString:
		literal, err := strconv.Unquote(token.Text)
		if err != nil {
			return "", fmt.Errorf("decode lark string literal: %w", err)
		}
		return strconv.Quote(literal), nil
	case larkTokenRegex:
		return token.Text, nil
	case larkTokenLParen:
		expr, err := p.parseAlternation()
		if err != nil {
			return "", err
		}
		if p.peek().Kind != larkTokenRParen {
			return "", fmt.Errorf("unclosed lark group")
		}
		p.next()
		return "(" + expr + ")", nil
	default:
		return "", fmt.Errorf("unexpected lark token %q", token.Text)
	}
}

func (p *larkVLLMGrammarExprParser) peek() larkToken {
	if p.position >= len(p.tokens) {
		return larkToken{Kind: larkTokenEOF}
	}
	return p.tokens[p.position]
}

func (p *larkVLLMGrammarExprParser) next() larkToken {
	token := p.peek()
	if p.position < len(p.tokens) {
		p.position++
	}
	return token
}

func isLarkIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if !isLarkIdentifierStart(r) {
				return false
			}
			continue
		}
		if !isLarkIdentifierPart(r) {
			return false
		}
	}
	return true
}

func isLarkIdentifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isLarkIdentifierPart(r rune) bool {
	return isLarkIdentifierStart(r) || unicode.IsDigit(r)
}
