package codexeval

const (
	SuiteScopeSmoke            = "smoke"
	SuiteScopeControl          = "control"
	SuiteScopeRealStable       = "real-stable"
	SuiteScopeRealExpanded     = "real-expanded"
	SuiteScopeProfile          = "profile"
	SuiteScopeRegressionImport = "regression-import"
	SuiteScopeCustom           = "custom"
)

func SuiteScope(suite string) string {
	switch suite {
	case "codex-smoke":
		return SuiteScopeSmoke
	case "codex-core":
		return SuiteScopeControl
	case "codex-real-upstream":
		return SuiteScopeRealStable
	case "codex-real-upstream-expanded":
		return SuiteScopeRealExpanded
	case "codex-core-shell", "codex-core-websocket", "codex-core-interactive", "codex-compat":
		return SuiteScopeProfile
	case "codex-regression-import":
		return SuiteScopeRegressionImport
	default:
		return SuiteScopeCustom
	}
}
