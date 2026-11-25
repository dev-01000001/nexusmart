package guardrail

// CheckResult is returned by Engine.CheckInput / CheckUntrustedData / FilterOutput.
type CheckResult struct {
	Allowed         bool
	Blocked         bool
	InjectionFound  bool
	Category        string
	Reason          string
	MatchedPatterns []string
	SanitizedText   string
}

// GroundedProduct identifies a retrieved product allowed to be referenced in output.
type GroundedProduct struct {
	ID     string
	ShopID string
	Name   string
}

// Engine enforces input validation, prompt-injection detection (direct & indirect),
// untrusted-data sanitization, and output secret/leak filtering.
type Engine interface {
	CheckUserInput(text string) CheckResult
	SanitizeUntrustedData(source, text string) CheckResult
	FilterOutput(text string) (clean string, redacted bool)
	VerifyGrounding(text string, allowedShopID string, retrieved []GroundedProduct) (hallucinated bool, reasons []string)
}
