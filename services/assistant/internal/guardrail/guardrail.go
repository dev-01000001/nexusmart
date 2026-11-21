package guardrail

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxUserInputRunes is the maximum allowed rune length for a user prompt.
	MaxUserInputRunes = 4000

	sanitizedPlaceholder = "[nội dung không hợp lệ đã bị lọc]"
	redactedPlaceholder  = "[REDACTED]"
)

// Compile-time check that DefaultEngine implements Engine.
var _ Engine = (*DefaultEngine)(nil)

type patternRule struct {
	category string
	name     string
	// substr matches against accent-folded, invisible-stripped lowercase text.
	substr string
	// reFolded matches against accent-folded, invisible-stripped lowercase text.
	reFolded *regexp.Regexp
	// reRaw matches against invisible-stripped text preserving original case/symbols.
	reRaw *regexp.Regexp
}

// DefaultEngine is a stateless, concurrency-safe implementation of Engine.
type DefaultEngine struct {
	directRules    []patternRule
	untrustedRules []patternRule
}

var (
	base64CandidateRe = regexp.MustCompile(`[A-Za-z0-9+/]{16,}={0,2}`)

	// Output secret & internal marker redaction patterns.
	outputSecretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
		regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
		regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`),
		regexp.MustCompile(`\bpa-[A-Za-z0-9_-]{8,}\b`),
		regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s"'<>]+`),
		regexp.MustCompile(`(?i)rediss?://[^\s"'<>]+`),
		regexp.MustCompile(`(?i)nats://[^\s"'<>]+`),
		regexp.MustCompile(`(?i)mysql://[^\s"'<>]+`),
		regexp.MustCompile(`(?i)(?:ANTHROPIC_API_KEY|JWT_SECRET|DATABASE_URL|REDIS_PASSWORD|VOYAGE_API_KEY)\s*[:=]\s*[^\s,;]+`),
		regexp.MustCompile(`NEXUSMART_SYSTEM_PROMPT`),
		regexp.MustCompile(`(?i)</?system_rules>`),
		regexp.MustCompile(`(?i)</?untrusted_[a-z0-9_]+>`),
		regexp.MustCompile(`(?i)<\|im_(?:start|end)\|>`),
	}

	// Grounding verification patterns.
	prodIDRe       = regexp.MustCompile(`(?i)\bprod-[a-z0-9][a-z0-9-]*\b`)
	skuIDRe        = regexp.MustCompile(`(?i)\bsku-[a-z0-9][a-z0-9-]*\b`)
	productUUIDRe  = regexp.MustCompile(`(?i)(?:product[_\s-]*id|san pham|ma san pham|id)\s*[:=#]?\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	vndPriceRe     = regexp.MustCompile(`(?i)\d[\d.,]*\s*(?:vnđ|vnd|đồng|₫|đ)(?:[\s.,;!?)\]]|$)`)
	inlineMarkerRe = regexp.MustCompile(`(?i)(<\|im_start\|>|<\|im_end\|>|</tool_result>|</untrusted_|\[INST\]|###\s*system|BEGIN\s+SYSTEM\s+PROMPT|(?:^|\s)SYSTEM\s*:)`)
)

// New constructs a DefaultEngine preloaded with Vietnamese and English prompt-injection
// detection rules, untrusted-data sanitization rules, secret scrubbers, and grounding checks.
func New() *DefaultEngine {
	direct := []patternRule{
		// Delimiter spoofing
		{category: "delimiter_spoofing", name: "<|im_start|>", reRaw: regexp.MustCompile(`(?i)<\|im_start\|>`)},
		{category: "delimiter_spoofing", name: "<|im_end|>", reRaw: regexp.MustCompile(`(?i)<\|im_end\|>`)},
		{category: "delimiter_spoofing", name: "<|system|>", reRaw: regexp.MustCompile(`(?i)<\|system\|>`)},
		{category: "delimiter_spoofing", name: "[INST]", reRaw: regexp.MustCompile(`(?i)\[/?INST\]`)},
		{category: "delimiter_spoofing", name: "<<SYS>>", reRaw: regexp.MustCompile(`(?i)<<\s*/?SYS\s*>>`)},
		{category: "delimiter_spoofing", name: "### SYSTEM", reRaw: regexp.MustCompile(`(?i)###\s*(?:system|instruction)`)},
		{category: "delimiter_spoofing", name: "</tool_result>", reRaw: regexp.MustCompile(`(?i)</tool_result>`)},
		{category: "delimiter_spoofing", name: "</untrusted_", reRaw: regexp.MustCompile(`(?i)</untrusted_`)},
		{category: "delimiter_spoofing", name: "BEGIN SYSTEM PROMPT", reRaw: regexp.MustCompile(`(?i)(?:begin|end)\s+system\s+prompt`)},
		{category: "delimiter_spoofing", name: "<system_rules>", reRaw: regexp.MustCompile(`(?i)</?system_rules>`)},

		// Instruction override (EN & VI)
		{category: "instruction_override", name: "ignore previous instructions", substr: "ignore previous instructions"},
		{category: "instruction_override", name: "ignore all previous", substr: "ignore all previous"},
		{category: "instruction_override", name: "ignore prior instructions", substr: "ignore prior instructions"},
		{category: "instruction_override", name: "ignore above instructions", substr: "ignore above instructions"},
		{category: "instruction_override", name: "ignore your instructions", substr: "ignore your instructions"},
		{category: "instruction_override", name: "ignore all rules", substr: "ignore all rules"},
		{category: "instruction_override", name: "disregard above", substr: "disregard above"},
		{category: "instruction_override", name: "disregard previous", substr: "disregard previous"},
		{category: "instruction_override", name: "disregard all rules", substr: "disregard all rules"},
		{category: "instruction_override", name: "override system", substr: "override system"},
		{category: "instruction_override", name: "override instructions", substr: "override instructions"},
		{category: "instruction_override", name: "override your rules", substr: "override your rules"},
		{category: "instruction_override", name: "new instructions", substr: "new instructions"},
		{category: "instruction_override", name: "forget everything", substr: "forget everything"},
		{category: "instruction_override", name: "forget previous", substr: "forget previous"},
		{category: "instruction_override", name: "forget all instructions", substr: "forget all instructions"},
		{category: "instruction_override", name: "forget your rules", substr: "forget your rules"},
		{category: "instruction_override", name: "bo qua huong dan", substr: "bo qua huong dan"},
		{category: "instruction_override", name: "bo qua moi huong dan", substr: "bo qua moi huong dan"},
		{category: "instruction_override", name: "bo qua tat ca huong dan", substr: "bo qua tat ca huong dan"},
		{category: "instruction_override", name: "bo qua moi quy tac", substr: "bo qua moi quy tac"},
		{category: "instruction_override", name: "bo qua tat ca quy tac", substr: "bo qua tat ca quy tac"},
		{category: "instruction_override", name: "bo qua quy tac", substr: "bo qua quy tac"},
		{category: "instruction_override", name: "bo qua chi dan", substr: "bo qua chi dan"},
		{category: "instruction_override", name: "bo qua lenh truoc", substr: "bo qua lenh truoc"},
		{category: "instruction_override", name: "quen tat ca quy tac", substr: "quen tat ca quy tac"},
		{category: "instruction_override", name: "quen moi quy tac", substr: "quen moi quy tac"},
		{category: "instruction_override", name: "quen het huong dan", substr: "quen het huong dan"},
		{category: "instruction_override", name: "quen huong dan", substr: "quen huong dan"},
		{category: "instruction_override", name: "quen tat ca", substr: "quen tat ca"},
		{category: "instruction_override", name: "huong dan moi:", substr: "huong dan moi:"},
		{category: "instruction_override", name: "chi dan moi:", substr: "chi dan moi:"},
		{category: "instruction_override", name: "ghi de he thong", substr: "ghi de he thong"},
		{category: "instruction_override", name: "ghi de huong dan", substr: "ghi de huong dan"},

		// Jailbreak (EN & VI)
		{category: "jailbreak", name: "developer mode", substr: "developer mode"},
		{category: "jailbreak", name: "che do nha phat trien", substr: "che do nha phat trien"},
		{category: "jailbreak", name: "DAN", reRaw: regexp.MustCompile(`\bDAN\b`)},
		{category: "jailbreak", name: "do anything now", substr: "do anything now"},
		{category: "jailbreak", name: "jailbreak", substr: "jailbreak"},
		{category: "jailbreak", name: "nhap vai admin", substr: "nhap vai admin"},
		{category: "jailbreak", name: "nhap vai quan tri", substr: "nhap vai quan tri"},
		{category: "jailbreak", name: "ban bay gio la admin", substr: "ban bay gio la admin"},
		{category: "jailbreak", name: "ban la admin", substr: "ban la admin"},
		{category: "jailbreak", name: "khong con gioi han", substr: "khong con gioi han"},
		{category: "jailbreak", name: "bo moi gioi han", substr: "bo moi gioi han"},
		{category: "jailbreak", name: "act as root", substr: "act as root"},
		{category: "jailbreak", name: "act as admin", substr: "act as admin"},
		{category: "jailbreak", name: "you are now admin", substr: "you are now admin"},
		{category: "jailbreak", name: "unrestricted mode", substr: "unrestricted mode"},
		{category: "jailbreak", name: "che do khong gioi han", substr: "che do khong gioi han"},
		{category: "jailbreak", name: "bypass safety", substr: "bypass safety"},
		{category: "jailbreak", name: "disable guardrail", substr: "disable guardrail"},

		// Prompt exfiltration (EN & VI)
		{category: "prompt_exfiltration", name: "in ra system prompt", substr: "in ra system prompt"},
		{category: "prompt_exfiltration", name: "in system prompt", substr: "in system prompt"},
		{category: "prompt_exfiltration", name: "reveal your system prompt", substr: "reveal your system prompt"},
		{category: "prompt_exfiltration", name: "reveal system prompt", substr: "reveal system prompt"},
		{category: "prompt_exfiltration", name: "hien thi prompt goc", substr: "hien thi prompt goc"},
		{category: "prompt_exfiltration", name: "hien thi system prompt", substr: "hien thi system prompt"},
		{category: "prompt_exfiltration", name: "show your instructions", substr: "show your instructions"},
		{category: "prompt_exfiltration", name: "show your system prompt", substr: "show your system prompt"},
		{category: "prompt_exfiltration", name: "show system prompt", substr: "show system prompt"},
		{category: "prompt_exfiltration", name: "cho xem system prompt", substr: "cho xem system prompt"},
		{category: "prompt_exfiltration", name: "cho xem prompt goc", substr: "cho xem prompt goc"},
		{category: "prompt_exfiltration", name: "tiet lo system prompt", substr: "tiet lo system prompt"},
		{category: "prompt_exfiltration", name: "xuat system prompt", substr: "xuat system prompt"},
		{category: "prompt_exfiltration", name: "print system message", substr: "print system message"},
		{category: "prompt_exfiltration", name: "print system prompt", substr: "print system prompt"},
		{category: "prompt_exfiltration", name: "print your instructions", substr: "print your instructions"},
		{category: "prompt_exfiltration", name: "repeat your system prompt", substr: "repeat your system prompt"},
		{category: "prompt_exfiltration", name: "dump system prompt", substr: "dump system prompt"},
		{category: "prompt_exfiltration", name: "in ra cau lenh he thong", substr: "in ra cau lenh he thong"},
		{category: "prompt_exfiltration", name: "hien thi huong dan goc", substr: "hien thi huong dan goc"},

		// Secret exfiltration (EN & VI)
		{category: "secret_exfiltration", name: "in ra jwt", substr: "in ra jwt"},
		{category: "secret_exfiltration", name: "xuat jwt", substr: "xuat jwt"},
		{category: "secret_exfiltration", name: "cho xem jwt", substr: "cho xem jwt"},
		{category: "secret_exfiltration", name: "gui jwt", substr: "gui jwt"},
		{category: "secret_exfiltration", name: "tiet lo jwt", substr: "tiet lo jwt"},
		{category: "secret_exfiltration", name: "xuat api key", substr: "xuat api key"},
		{category: "secret_exfiltration", name: "in ra api key", substr: "in ra api key"},
		{category: "secret_exfiltration", name: "cho xem api key", substr: "cho xem api key"},
		{category: "secret_exfiltration", name: "gui api key", substr: "gui api key"},
		{category: "secret_exfiltration", name: "show api key", substr: "show api key"},
		{category: "secret_exfiltration", name: "print api key", substr: "print api key"},
		{category: "secret_exfiltration", name: "reveal api key", substr: "reveal api key"},
		{category: "secret_exfiltration", name: "anthropic_api_key", substr: "anthropic_api_key"},
		{category: "secret_exfiltration", name: "database_url", substr: "database_url"},
		{category: "secret_exfiltration", name: "jwt_secret", substr: "jwt_secret"},
		{category: "secret_exfiltration", name: "voyage_api_key", substr: "voyage_api_key"},
		{category: "secret_exfiltration", name: "redis_password", substr: "redis_password"},
		{category: "secret_exfiltration", name: "bien moi truong", substr: "bien moi truong"},
		{category: "secret_exfiltration", name: "print env", substr: "print env"},
		{category: "secret_exfiltration", name: "show env", substr: "show env"},
		{category: "secret_exfiltration", name: "dump env", substr: "dump env"},
		{category: "secret_exfiltration", name: "show bearer token", substr: "show bearer token"},
		{category: "secret_exfiltration", name: "print bearer token", substr: "print bearer token"},
		{category: "secret_exfiltration", name: "show jwt", substr: "show jwt"},
		{category: "secret_exfiltration", name: "send their jwt", substr: "send their jwt"},
		{category: "secret_exfiltration", name: "send jwt", substr: "send jwt"},

		// Privilege escalation (EN & VI)
		{category: "privilege_escalation", name: "tu thanh toan", substr: "tu thanh toan"},
		{category: "privilege_escalation", name: "thanh toan ho", substr: "thanh toan ho"},
		{category: "privilege_escalation", name: "tu dong thanh toan", substr: "tu dong thanh toan"},
		{category: "privilege_escalation", name: "tu checkout", substr: "tu checkout"},
		{category: "privilege_escalation", name: "truy van sql", substr: "truy van sql"},
		{category: "privilege_escalation", name: "chay sql", substr: "chay sql"},
		{category: "privilege_escalation", name: "select * from", substr: "select * from"},
		{category: "privilege_escalation", name: "drop table", substr: "drop table"},
		{category: "privilege_escalation", name: "union select", substr: "union select"},
		{category: "privilege_escalation", name: "delete from", reFolded: regexp.MustCompile(`\bdelete\s+from\s+[a-z_]+`)},
		{category: "privilege_escalation", name: "xem don cua nguoi khac", substr: "xem don cua nguoi khac"},
		{category: "privilege_escalation", name: "xem don hang cua nguoi khac", substr: "xem don hang cua nguoi khac"},
		{category: "privilege_escalation", name: "don hang cua user khac", substr: "don hang cua user khac"},
		{category: "privilege_escalation", name: "don cua user khac", substr: "don cua user khac"},
		{category: "privilege_escalation", name: "bo loc shop_id", substr: "bo loc shop_id"},
		{category: "privilege_escalation", name: "bo qua shop_id", substr: "bo qua shop_id"},
		{category: "privilege_escalation", name: "xem gian hang khac", substr: "xem gian hang khac"},
		{category: "privilege_escalation", name: "bypass shop_id", substr: "bypass shop_id"},
		{category: "privilege_escalation", name: "dat gia 0", substr: "dat gia 0"},
		{category: "privilege_escalation", name: "sua gia 0", substr: "sua gia 0"},
		{category: "privilege_escalation", name: "giam gia 100%", substr: "giam gia 100%"},
		{category: "privilege_escalation", name: "giam 100%", substr: "giam 100%"},
	}

	// Extra rules specific to indirect prompt injection in untrusted data
	// (product descriptions, reviews, buyer chat messages).
	untrustedExtra := []patternRule{
		{category: "indirect_injection", name: "bao gia 0d", reFolded: regexp.MustCompile(`bao\s+gia\s+0\s*(?:d|dong|vnd)?\b`)},
		{category: "indirect_injection", name: "gia 0d", reFolded: regexp.MustCompile(`(?:^|[^\d.,])0\s*(?:d|dong|vnd)(?:\b|$)`)},
		{category: "indirect_injection", name: "item is 0", reFolded: regexp.MustCompile(`(?:price|item|product)\s+(?:is|to)\s+0\b`)},
		{category: "indirect_injection", name: "mien phi 100%", substr: "mien phi 100%"},
		{category: "indirect_injection", name: "100% free", substr: "100% free"},
		{category: "indirect_injection", name: "tang mien phi toan bo", substr: "tang mien phi toan bo"},
		{category: "indirect_injection", name: "giam 99%", substr: "giam 99%"},
		{category: "indirect_injection", name: "giam gia 99%", substr: "giam gia 99%"},
		{category: "indirect_injection", name: "gui link", substr: "gui link"},
		{category: "indirect_injection", name: "chen link", substr: "chen link"},
		{category: "indirect_injection", name: "chuyen khoan vao stk", substr: "chuyen khoan vao stk"},
		{category: "indirect_injection", name: "chuyen khoan vao so tai khoan", substr: "chuyen khoan vao so tai khoan"},
		{category: "indirect_injection", name: "gui so tai khoan", substr: "gui so tai khoan"},
		{category: "indirect_injection", name: "nhan vao duong dan", substr: "nhan vao duong dan"},
		{category: "indirect_injection", name: "bam vao link", substr: "bam vao link"},
		{category: "indirect_injection", name: "hoan tien ngay khong can duyet", substr: "hoan tien ngay khong can duyet"},
		{category: "indirect_injection", name: "hoan tien 100% khong can tra hang", substr: "hoan tien 100% khong can tra hang"},
		{category: "indirect_injection", name: "dong y hoan tien 100%", substr: "dong y hoan tien 100%"},
		{category: "indirect_injection", name: "them sku vao gio khong can hoi", reFolded: regexp.MustCompile(`them\s+sku[\w\s-]*vao\s+gio\s+khong\s+can\s+hoi`)},
		{category: "indirect_injection", name: "vao gio khong can hoi", substr: "vao gio khong can hoi"},
		{category: "indirect_injection", name: "add_to_cart", substr: "add_to_cart"},
		{category: "indirect_injection", name: "always offer", substr: "always offer"},
		{category: "indirect_injection", name: "always tell the user", substr: "always tell the user"},
		{category: "indirect_injection", name: "hay goi y nguoi ban", substr: "hay goi y nguoi ban"},
		{category: "indirect_injection", name: "SYSTEM:", reRaw: regexp.MustCompile(`(?i)(?:^|\s)SYSTEM\s*:`)},
		{category: "indirect_injection", name: "suspicious_url", reRaw: regexp.MustCompile(`(?i)(?:https?://(?:evil[^\s]*|[^\s]*phishing[^\s]*)|(?:https?://)?(?:bit\.ly|tinyurl\.com|tinyurl|evil\.com|evil\.example)/\S*|\b(?:evil\.com|evil\.example|phishing\.test)\b)`)},
	}

	untrusted := make([]patternRule, 0, len(direct)+len(untrustedExtra))
	untrusted = append(untrusted, direct...)
	untrusted = append(untrusted, untrustedExtra...)

	return &DefaultEngine{
		directRules:    direct,
		untrustedRules: untrusted,
	}
}

// CheckUserInput validates user message length and scans for direct prompt injection,
// invisible-character obfuscation, and base64-encoded override payloads.
func (e *DefaultEngine) CheckUserInput(text string) CheckResult {
	if utf8.RuneCountInString(text) > MaxUserInputRunes {
		return CheckResult{
			Allowed:  false,
			Blocked:  true,
			Category: "input_too_long",
			Reason:   "Tin nhắn vượt quá giới hạn 4000 ký tự.",
		}
	}

	stripped := stripInvisible(text)
	if strings.TrimSpace(stripped) == "" {
		return CheckResult{
			Allowed:  false,
			Blocked:  true,
			Category: "empty_input",
			Reason:   "Nội dung tin nhắn không được để trống.",
		}
	}

	cat, matched := e.scanText(stripped, e.directRules, true)
	if len(matched) > 0 {
		return CheckResult{
			Allowed:         false,
			Blocked:         true,
			InjectionFound:  true,
			Category:        cat,
			Reason:          "Yêu cầu chứa nội dung không hợp lệ hoặc cố gắng can thiệp hệ thống.",
			MatchedPatterns: matched,
		}
	}

	return CheckResult{
		Allowed:       true,
		SanitizedText: stripped,
	}
}

// SanitizeUntrustedData scans untrusted data (product descriptions, reviews, buyer chat)
// for direct and indirect prompt injection, replacing matching sentences/lines with
// a safe placeholder while preserving benign text.
func (e *DefaultEngine) SanitizeUntrustedData(source, text string) CheckResult {
	stripped := stripInvisible(text)
	if strings.TrimSpace(stripped) == "" {
		return CheckResult{
			Allowed:       true,
			SanitizedText: strings.TrimSpace(stripped),
		}
	}

	var allMatched []string
	seen := make(map[string]struct{})
	firstCategory := ""
	injectionFound := false

	record := func(cat string, pats []string) {
		if len(pats) == 0 {
			return
		}
		injectionFound = true
		if firstCategory == "" {
			firstCategory = cat
		}
		for _, p := range pats {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				allMatched = append(allMatched, p)
			}
		}
	}

	rawLines := strings.Split(stripped, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}

	// Detect payloads intentionally split across adjacent lines (e.g. "ignore previous\ninstructions").
	linePoisoned := make([]bool, len(lines))
	for i := 0; i+1 < len(lines); i++ {
		_, pA := e.scanText(lines[i], e.untrustedRules, true)
		_, pB := e.scanText(lines[i+1], e.untrustedRules, true)
		catAB, pAB := e.scanText(lines[i]+" "+lines[i+1], e.untrustedRules, true)
		if hasNewPattern(pAB, pA, pB) {
			record(catAB, pAB)
			linePoisoned[i] = true
			linePoisoned[i+1] = true
		}
	}

	cleanedLines := make([]string, 0, len(lines))
	lastLinePlaceholder := false
	for i, trimmedLine := range lines {
		if linePoisoned[i] {
			if !lastLinePlaceholder {
				cleanedLines = append(cleanedLines, sanitizedPlaceholder)
				lastLinePlaceholder = true
			}
			continue
		}
		segments := splitUntrustedSegments(trimmedLine)
		var outSegs []string
		lastPlaceholder := false
		for _, seg := range segments {
			s := strings.TrimSpace(seg)
			if s == "" {
				continue
			}
			cat, pats := e.scanText(s, e.untrustedRules, true)
			if len(pats) > 0 {
				record(cat, pats)
				if !lastPlaceholder {
					outSegs = append(outSegs, sanitizedPlaceholder)
					lastPlaceholder = true
				}
				continue
			}
			outSegs = append(outSegs, s)
			lastPlaceholder = false
		}
		if len(outSegs) > 0 {
			joined := strings.Join(outSegs, " ")
			if joined == sanitizedPlaceholder {
				if !lastLinePlaceholder {
					cleanedLines = append(cleanedLines, joined)
					lastLinePlaceholder = true
				}
			} else {
				cleanedLines = append(cleanedLines, joined)
				lastLinePlaceholder = false
			}
		}
	}

	sanitized := strings.Join(cleanedLines, "\n")

	// Final full-text check catches multi-line payloads spanning >2 lines.
	if cat, pats := e.scanText(sanitized, e.untrustedRules, true); len(pats) > 0 {
		record(cat, pats)
		sanitized = sanitizedPlaceholder
	}

	res := CheckResult{
		Allowed:         true,
		InjectionFound:  injectionFound,
		Category:        firstCategory,
		MatchedPatterns: allMatched,
		SanitizedText:   sanitized,
	}
	if injectionFound {
		res.Reason = "Phát hiện nội dung chèn lệnh gián tiếp trong " + source
	}
	return res
}

// FilterOutput scrubs secrets, JWTs, API keys, DSNs, and internal prompt markers from output.
func (e *DefaultEngine) FilterOutput(text string) (string, bool) {
	if text == "" {
		return "", false
	}
	clean := text
	for _, re := range outputSecretPatterns {
		clean = re.ReplaceAllString(clean, redactedPlaceholder)
	}
	return clean, clean != text
}

// VerifyGrounding checks whether product/SKU IDs or product prices in text are grounded
// in the retrieved product set and scoped to allowedShopID.
func (e *DefaultEngine) VerifyGrounding(text string, allowedShopID string, retrieved []GroundedProduct) (bool, []string) {
	byID := make(map[string]GroundedProduct, len(retrieved))
	prodStems := make(map[string]GroundedProduct, len(retrieved))
	for _, p := range retrieved {
		idLower := strings.ToLower(strings.TrimSpace(p.ID))
		if idLower == "" {
			continue
		}
		byID[idLower] = p
		if strings.HasPrefix(idLower, "prod-") {
			prodStems[strings.TrimPrefix(idLower, "prod-")] = p
		}
	}

	var reasons []string
	reasonSeen := make(map[string]struct{})
	addReason := func(r string) {
		if _, ok := reasonSeen[r]; !ok {
			reasonSeen[r] = struct{}{}
			reasons = append(reasons, r)
		}
	}

	// 1. Check prod-* IDs.
	for _, match := range prodIDRe.FindAllString(text, -1) {
		id := strings.ToLower(match)
		p, ok := byID[id]
		if !ok {
			addReason("ungrounded product id: " + match)
			continue
		}
		if allowedShopID != "" && p.ShopID != "" && !strings.EqualFold(p.ShopID, allowedShopID) {
			addReason("cross-shop product id: " + match)
		}
	}

	// 2. Check sku-* IDs.
	for _, match := range skuIDRe.FindAllString(text, -1) {
		id := strings.ToLower(match)
		if p, ok := byID[id]; ok {
			if allowedShopID != "" && p.ShopID != "" && !strings.EqualFold(p.ShopID, allowedShopID) {
				addReason("cross-shop sku id: " + match)
			}
			continue
		}
		// Allow SKU IDs whose stem matches a retrieved prod-* ID in the allowed shop.
		stem := strings.TrimPrefix(id, "sku-")
		matchedStem := false
		wrongShop := false
		for pStem, p := range prodStems {
			if stem == pStem || strings.HasPrefix(stem, pStem+"-") {
				if allowedShopID != "" && p.ShopID != "" && !strings.EqualFold(p.ShopID, allowedShopID) {
					wrongShop = true
				} else {
					matchedStem = true
				}
			}
		}
		if wrongShop && !matchedStem {
			addReason("cross-shop sku id: " + match)
		} else if !matchedStem {
			addReason("ungrounded sku id: " + match)
		}
	}

	// 3. Check UUIDs explicitly referenced as product IDs.
	folded := foldVietnamese(stripInvisible(text))
	for _, sub := range productUUIDRe.FindAllStringSubmatch(folded, -1) {
		if len(sub) < 2 {
			continue
		}
		uuid := strings.ToLower(sub[1])
		p, ok := byID[uuid]
		if !ok {
			addReason("ungrounded product uuid: " + uuid)
			continue
		}
		if allowedShopID != "" && p.ShopID != "" && !strings.EqualFold(p.ShopID, allowedShopID) {
			addReason("cross-shop product uuid: " + uuid)
		}
	}

	// 4. When no products were retrieved, flag ungrounded product recommendations with VND prices.
	if len(retrieved) == 0 && vndPriceRe.MatchString(text) {
		exemptions := []string{
			"chua co",
			"khong co",
			"khong tim thay",
			"chinh sach",
			"cod",
			"toi da",
			"bao cao",
			"doanh thu",
			"so du",
			"trang thai don hang",
			"ma don",
			"don hang #",
			"tong don",
			"da them vao gio",
			"xac nhan them",
		}
		exempt := false
		for _, ex := range exemptions {
			if strings.Contains(folded, ex) {
				exempt = true
				break
			}
		}
		if !exempt {
			addReason("potential hallucinated product price with empty retrieval")
		}
	}

	return len(reasons) > 0, reasons
}

func (e *DefaultEngine) scanText(stripped string, rules []patternRule, checkBase64 bool) (string, []string) {
	folded := foldVietnamese(stripped)

	var matched []string
	seen := make(map[string]struct{})
	firstCat := ""

	addMatch := func(cat, name string) {
		if firstCat == "" {
			firstCat = cat
		}
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			matched = append(matched, name)
		}
	}

	for _, r := range rules {
		switch {
		case r.substr != "":
			if strings.Contains(folded, r.substr) {
				addMatch(r.category, r.name)
			}
		case r.reFolded != nil:
			if r.reFolded.MatchString(folded) {
				addMatch(r.category, r.name)
			}
		case r.reRaw != nil:
			if r.reRaw.MatchString(stripped) {
				addMatch(r.category, r.name)
			}
		}
	}

	if checkBase64 {
		for _, cand := range base64CandidateRe.FindAllString(stripped, -1) {
			decoded, ok := decodePlausibleBase64(cand)
			if !ok {
				continue
			}
			innerStripped := stripInvisible(decoded)
			innerCat, innerPats := e.scanText(innerStripped, rules, false)
			if len(innerPats) > 0 {
				if firstCat == "" {
					firstCat = innerCat
				}
				for _, p := range innerPats {
					addMatch(innerCat, "base64:"+p)
				}
			}
		}
	}

	return firstCat, matched
}

func decodePlausibleBase64(candidate string) (string, bool) {
	if len(candidate) < 16 || len(candidate) > 4096 {
		return "", false
	}
	var raw []byte
	var err error
	if strings.ContainsRune(candidate, '=') {
		raw, err = base64.StdEncoding.DecodeString(candidate)
	} else {
		raw, err = base64.RawStdEncoding.DecodeString(candidate)
	}
	if err != nil || len(raw) < 8 || !utf8.Valid(raw) {
		return "", false
	}
	total := 0
	printable := 0
	for _, r := range string(raw) {
		total++
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			printable++
		}
	}
	if total == 0 || printable*100/total < 85 {
		return "", false
	}
	return string(raw), true
}

func hasNewPattern(combined, a, b []string) bool {
	if len(combined) == 0 {
		return false
	}
	existing := make(map[string]struct{}, len(a)+len(b))
	for _, p := range a {
		existing[p] = struct{}{}
	}
	for _, p := range b {
		existing[p] = struct{}{}
	}
	for _, p := range combined {
		if _, ok := existing[p]; !ok {
			return true
		}
	}
	return false
}

func splitUntrustedSegments(line string) []string {
	// First split right before embedded delimiter/system markers so benign prefixes stay intact.
	locs := inlineMarkerRe.FindAllStringIndex(line, -1)
	var chunks []string
	if len(locs) == 0 {
		chunks = []string{line}
	} else {
		prev := 0
		for _, loc := range locs {
			if loc[0] > prev {
				chunks = append(chunks, line[prev:loc[0]])
			}
			prev = loc[0]
		}
		if prev < len(line) {
			chunks = append(chunks, line[prev:])
		}
	}

	// Next split each chunk into sentences on punctuation followed by space.
	var segments []string
	for _, ch := range chunks {
		start := 0
		for i := 0; i < len(ch); i++ {
			b := ch[i]
			if (b == '.' || b == '!' || b == '?' || b == ';') && i+1 < len(ch) && ch[i+1] == ' ' {
				// Avoid splitting inside numeric values like "250.000".
				if b == '.' && i > 0 && ch[i-1] >= '0' && ch[i-1] <= '9' {
					continue
				}
				segments = append(segments, ch[start:i+1])
				start = i + 2
				i++
			}
		}
		if start < len(ch) {
			segments = append(segments, ch[start:])
		}
	}
	return segments
}

func stripInvisible(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) || r == '\u180e' || r == '\u034f' {
			continue
		}
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func foldVietnamese(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		mapped := foldRune(r)
		if unicode.IsSpace(mapped) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		lastSpace = false
		b.WriteRune(mapped)
	}
	return strings.TrimSpace(b.String())
}

func foldRune(r rune) rune {
	switch r {
	case 'á', 'à', 'ả', 'ã', 'ạ', 'ă', 'ắ', 'ằ', 'ẳ', 'ẵ', 'ặ', 'â', 'ấ', 'ầ', 'ẩ', 'ẫ', 'ậ':
		return 'a'
	case 'é', 'è', 'ẻ', 'ẽ', 'ẹ', 'ê', 'ế', 'ề', 'ể', 'ễ', 'ệ':
		return 'e'
	case 'í', 'ì', 'ỉ', 'ĩ', 'ị':
		return 'i'
	case 'ó', 'ò', 'ỏ', 'õ', 'ọ', 'ô', 'ố', 'ồ', 'ổ', 'ỗ', 'ộ', 'ơ', 'ớ', 'ờ', 'ở', 'ỡ', 'ợ':
		return 'o'
	case 'ú', 'ù', 'ủ', 'ũ', 'ụ', 'ư', 'ứ', 'ừ', 'ử', 'ữ', 'ự':
		return 'u'
	case 'ý', 'ỳ', 'ỷ', 'ỹ', 'ỵ':
		return 'y'
	case 'đ':
		return 'd'
	default:
		return r
	}
}
