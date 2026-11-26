package moderation

import (
	"math/bits"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// minTermCompactRunes is the minimum normalized compact length accepted for a banned keyword rule.
	minTermCompactRunes = 2
	// minCompactMatchRunes is the minimum compact keyword length eligible for E9 rules (b) and (c).
	minCompactMatchRunes = 4
	// maxShortTokenRunes is the maximum token rune length merged into contiguous runs in E9 rule (c).
	maxShortTokenRunes = 2
	// ponytail: trần ghép (maxMergeRawFactor*len(kw)+maxMergeRawPad), chuỗi lặp dài hơn chỉ bị luật (c) bắt.
	maxMergeRawFactor = 4
	maxMergeRawPad    = 8

	vietLat1Start, vietLat1End = 0x00C0, 0x01B0
	vietExtStart, vietExtEnd   = 0x1EA0, 0x1EF9
)

var (
	asciiParsed [128]parsedChar
	vietLat1    [vietLat1End - vietLat1Start + 1]parsedChar
	vietExt     [vietExtEnd - vietExtStart + 1]parsedChar
)

func init() {
	for b := 0; b < 128; b++ {
		lr := unicode.ToLower(rune(b))
		fr := foldLeet(lr)
		asciiParsed[b] = parsedChar{base: fr, shaped: fr, leet: fr != lr, isAlnum: (fr >= 'a' && fr <= 'z') || (fr >= '0' && fr <= '9')}
	}
	const vietLetters = "ÀÁÂÃÈÉÊÌÍÒÓÔÕÙÚÝàáâãèéêìíòóôõùúýĂăĐđĨĩŨũƠơƯư" +
		"ẠạẢảẤấẦầẨẩẪẫẬậẮắẰằẲẳẴẵẶặẸẹẺẻẼẽẾếỀềỂểỄễỆệỈỉỊị" +
		"ỌọỎỏỐốỒồỔổỖỗỘộỚớỜờỞởỠỡỢợỤụỦủỨứỪừỬửỮữỰựỲỳỴỵỶỷỸỹ"
	for _, r := range vietLetters {
		c := parsedChar{base: 'd', shaped: 'đ', isAlnum: true}
		if r != 'Đ' && r != 'đ' {
			for _, nr := range norm.NFKD.String(string(r)) {
				if !isCombiningMark(nr) {
					lr := unicode.ToLower(nr)
					c = parsedChar{base: lr, shaped: lr, isAlnum: true}
				} else if bit := vietnameseToneBit(nr); bit != 0 {
					c.toneMask |= bit
				} else {
					applyVietnameseQuality(&c, nr)
				}
			}
		}
		if r >= vietExtStart {
			vietExt[r-vietExtStart] = c
		} else {
			vietLat1[r-vietLat1Start] = c
		}
	}
}

func lookupVietnameseChar(r rune) (parsedChar, bool) {
	if r >= vietExtStart && r <= vietExtEnd {
		return vietExt[r-vietExtStart], vietExt[r-vietExtStart].isAlnum
	}
	if r >= vietLat1Start && r <= vietLat1End {
		return vietLat1[r-vietLat1Start], vietLat1[r-vietLat1Start].isAlnum
	}
	return parsedChar{}, false
}

const (
	toneGrave uint8 = 1 << iota // huyền (\u0300)
	toneAcute                   // sắc   (\u0301)
	toneHook                    // hỏi   (\u0309)
	toneTilde                   // ngã   (\u0303)
	toneDot                     // nặng  (\u0323)
)

// Action represents a moderation rule enforcement action.
type Action string

const (
	ActionBlock  Action = "BLOCK"
	ActionReview Action = "REVIEW"
)

// Rule defines a banned keyword rule evaluated by the E9 moderation matcher.
type Rule struct {
	ID     string
	Term   string
	Action Action
}

// parsedChar is a single normalized character emitted by the single-pass NFKD pipeline.
type parsedChar struct {
	base     rune  // lowercase ASCII/folded base ('d' for 'đ', 'a' for 'ầ', etc.)
	shaped   rune  // preserves Vietnamese vowel shape (â, ă, ê, ô, ơ, ư) and đ; strips tone marks
	toneMask uint8 // bitmask of Vietnamese tone marks attached to this vowel
	leet     bool  // true if derived from leetspeak folding
	conflict bool  // true if >=3 repeat collapse merged conflicting Vietnamese vowel shapes
	isAlnum  bool  // letter or digit (token character)
}

type normToken struct {
	raw          string // accent-preserved normalized token (populated only when needRaw=true)
	shaped       string // toneless Vietnamese shape (preserves â ă ê ô ơ ư đ)
	folded       string // diacritic-folded (+ >=3 repeat collapsed) token
	chars        []parsedChar
	toneMask     uint8
	multiTone    bool // >1 distinct Vietnamese tones (or conflicting collapsed vowel shapes) in token
	hasDiacritic bool
	hadLeet      bool // true if any rune in the token came from leetspeak folding
	runeLen      int
}

// CompiledRule holds the pre-normalized representation of a banned keyword Rule.
type CompiledRule struct {
	Rule
	NormalizedTerm          string
	tokens                  []normToken
	compact                 string
	compactChars            []parsedChar
	compactToneMask         uint8
	compactRuneLen          int
	bigrams                 [26]uint32 // bits 0..25: intra-token bigram a->b; bit 26: letter a present
	minBigrams              [26]uint32 // necessary bigrams across rules (a), (b), and (c) for early skipping
	hasDiacritic            bool
	allowCompactSingleToken bool
}

// MatchResult summarizes all rules matched across scanned fields.
type MatchResult struct {
	Blocked       bool
	Flagged       bool
	BlockedTerms  []string
	FlaggedTerms  []string
	MatchedTerms  []string
	HighestAction Action
}

// NormalizeTerm normalizes a banned keyword term according to E9 and reports whether it is valid
// (non-empty and at least 2 alphanumeric runes in compact form).
func NormalizeTerm(term string) (string, bool) {
	tokens := tokenize(term, true)
	if len(tokens) == 0 {
		return "", false
	}
	rawParts := make([]string, len(tokens))
	compactRunes := 0
	for i, t := range tokens {
		rawParts[i] = t.raw
		compactRunes += t.runeLen
	}
	if compactRunes < minTermCompactRunes {
		return "", false
	}
	return strings.Join(rawParts, " "), true
}

// CompileRule pre-compiles a single Rule for repeated evaluation.
func CompileRule(r Rule) (CompiledRule, bool) {
	tokens := tokenize(r.Term, true)
	if len(tokens) == 0 {
		return CompiledRule{}, false
	}
	rawParts := make([]string, len(tokens))
	totalRawChars := 0
	hasDiacritic := false
	for i, t := range tokens {
		rawParts[i] = t.raw
		totalRawChars += len(t.chars)
		if t.hasDiacritic {
			hasDiacritic = true
		}
	}
	if totalRawChars < minTermCompactRunes {
		return CompiledRule{}, false
	}

	joinedChars := make([]parsedChar, 0, totalRawChars)
	for _, t := range tokens {
		joinedChars = append(joinedChars, t.chars...)
	}
	compactChars := collapseParsedChars3Into(joinedChars, make([]parsedChar, 0, len(joinedChars)))
	compactRuneLen := len(compactChars)
	if compactRuneLen < minTermCompactRunes {
		return CompiledRule{}, false
	}

	var compactBuf strings.Builder
	compactBuf.Grow(compactRuneLen)
	var compactToneMask uint8
	for _, c := range compactChars {
		compactBuf.WriteRune(c.base)
		compactToneMask |= c.toneMask
	}
	compact := compactBuf.String()

	var bigrams [26]uint32
	for _, t := range tokens {
		addTokenBigrams(&bigrams, t.chars)
	}
	minBigrams := bigrams
	if compactRuneLen >= minCompactMatchRunes {
		var compactBigrams [26]uint32
		addTokenBigrams(&compactBigrams, compactChars)
		for i := range minBigrams {
			minBigrams[i] &= compactBigrams[i]
		}
	}

	// For single-token accented rules (e.g. "súng"), do not let folded rule (b)
	// bypass rule (a)'s exact-diacritic check (e.g. "bổ sung" must not match "súng").
	allowCompactSingle := compactRuneLen >= minCompactMatchRunes && (len(tokens) > 1 || !tokens[0].hasDiacritic)

	return CompiledRule{
		Rule:                    r,
		NormalizedTerm:          strings.Join(rawParts, " "),
		tokens:                  tokens,
		compact:                 compact,
		compactChars:            compactChars,
		compactToneMask:         compactToneMask,
		compactRuneLen:          compactRuneLen,
		bigrams:                 bigrams,
		minBigrams:              minBigrams,
		hasDiacritic:            hasDiacritic,
		allowCompactSingleToken: allowCompactSingle,
	}, true
}

// CompileRules compiles a slice of rules, skipping invalid/empty ones.
func CompileRules(rules []Rule) []CompiledRule {
	out := make([]CompiledRule, 0, len(rules))
	for _, r := range rules {
		if cr, ok := CompileRule(r); ok {
			out = append(out, cr)
		}
	}
	return out
}

// Match compiles rules and scans each field independently (no cross-field token stitching).
func Match(rules []Rule, fields ...string) MatchResult {
	return MatchCompiled(CompileRules(rules), fields...)
}

// MatchCompiled scans each field independently against pre-compiled rules (E9).
func MatchCompiled(rules []CompiledRule, fields ...string) MatchResult {
	if len(rules) == 0 || len(fields) == 0 {
		return MatchResult{}
	}

	matched := make([]bool, len(rules))
	matchedCount := 0
	for _, field := range fields {
		if field == "" {
			continue
		}
		if fb, fastOK := scanFastFieldBigrams(field); fastOK && !anyRuleMatchesBigrams(rules, matched, &fb) {
			continue
		}
		toks := tokenize(field, false)
		if len(toks) == 0 {
			continue
		}
		doc := buildDocTokens(toks)
		for i := range rules {
			if !matched[i] && ruleMatchesDoc(&rules[i], &doc) {
				matched[i] = true
				matchedCount++
			}
		}
		if matchedCount == len(rules) {
			break
		}
	}
	if matchedCount == 0 {
		return MatchResult{}
	}

	blockedSet := make(map[string]struct{})
	flaggedSet := make(map[string]struct{})
	allSet := make(map[string]struct{}, matchedCount)
	for i := range rules {
		if !matched[i] {
			continue
		}
		rule := &rules[i]
		term := strings.TrimSpace(rule.Term)
		if term == "" {
			term = rule.NormalizedTerm
		}
		allSet[term] = struct{}{}
		switch rule.Action {
		case ActionBlock:
			blockedSet[term] = struct{}{}
		case ActionReview:
			flaggedSet[term] = struct{}{}
		}
	}

	res := MatchResult{
		Blocked:      len(blockedSet) > 0,
		Flagged:      len(flaggedSet) > 0,
		BlockedTerms: sortedKeys(blockedSet),
		FlaggedTerms: sortedKeys(flaggedSet),
		MatchedTerms: sortedKeys(allSet),
	}
	if res.Blocked {
		res.HighestAction = ActionBlock
	} else if res.Flagged {
		res.HighestAction = ActionReview
	}
	return res
}

func anyRuleMatchesBigrams(rules []CompiledRule, matched []bool, fb *[26]uint32) bool {
	for i := range rules {
		if !matched[i] && hasAllBigrams(fb, &rules[i].minBigrams) {
			return true
		}
	}
	return false
}

// scanFastFieldBigrams computes a sound superset of doc.bigrams without allocating when input
// consists solely of ASCII bytes and precomposed Vietnamese letters.
func scanFastFieldBigrams(input string) (bigrams [26]uint32, ok bool) {
	var inTok bool
	var tokLen, runLen int
	var tokFirst, tokPrev, prevShort byte
	flushShort := func() {
		if tokLen <= maxShortTokenRunes {
			if prevShort >= 'a' && prevShort <= 'z' && tokFirst >= 'a' && tokFirst <= 'z' {
				bigrams[prevShort-'a'] |= 1 << (tokFirst - 'a')
			}
			prevShort = tokPrev
		} else {
			prevShort = 0
		}
		inTok = false
	}
	for i := 0; i < len(input); {
		var c parsedChar
		if b := input[i]; b < utf8.RuneSelf {
			c, i = asciiParsed[b], i+1
		} else {
			r, size := utf8.DecodeRuneInString(input[i:])
			vc, vok := lookupVietnameseChar(r)
			if !vok {
				return [26]uint32{}, false
			}
			c, i = vc, i+size
		}
		if !c.isAlnum {
			if inTok {
				flushShort()
			}
			continue
		}
		base := byte(c.base)
		if base >= 'a' && base <= 'z' {
			bigrams[base-'a'] |= 1 << 26
			if inTok && tokPrev >= 'a' && tokPrev <= 'z' {
				bigrams[tokPrev-'a'] |= 1 << (base - 'a')
			}
		}
		if !inTok {
			inTok, tokFirst, tokPrev, runLen, tokLen = true, base, base, 1, 1
		} else if base == tokPrev {
			runLen++
			if runLen == 3 {
				tokLen--
			}
		} else {
			tokPrev, runLen, tokLen = base, 1, tokLen+1
		}
	}
	if inTok {
		flushShort()
	}
	return bigrams, true
}

type shortRun struct {
	tokens     []normToken
	foldedFlat string // collapseRepeats3 of concatenated t.folded (fast pre-filter)
}

type docTokens struct {
	tokens      []normToken
	positions   map[string][]int // t.folded -> ascending token indices
	shortStarts map[rune][]int   // first base rune -> ascending indices of tokens with runeLen <= 2
	shortRuns   []shortRun       // maximal contiguous <=2-rune token runs (>=4 collapsed runes)
	foldedSet   map[string]struct{}
	bigrams     [26]uint32 // bits 0..25: letter bigram a->b (within token or adjacent <=2-rune tokens); bit 26: presence
	hasFolded4  bool
}

func buildDocTokens(tokens []normToken) docTokens {
	dt := docTokens{
		tokens:    tokens,
		positions: make(map[string][]int, len(tokens)),
		foldedSet: make(map[string]struct{}, len(tokens)),
	}
	var runBuf strings.Builder
	runStart := -1
	flushRun := func(end int) {
		if runStart < 0 {
			return
		}
		merged := collapseRepeats3(runBuf.String())
		if utf8.RuneCountInString(merged) >= minCompactMatchRunes {
			dt.shortRuns = append(dt.shortRuns, shortRun{
				tokens:     tokens[runStart:end],
				foldedFlat: merged,
			})
		}
		runBuf.Reset()
		runStart = -1
	}

	prevShort := rune(0)
	for i, t := range tokens {
		dt.positions[t.folded] = append(dt.positions[t.folded], i)
		addTokenBigrams(&dt.bigrams, t.chars)
		if t.runeLen >= minCompactMatchRunes {
			dt.foldedSet[t.folded] = struct{}{}
			dt.hasFolded4 = true
		}
		if t.runeLen <= maxShortTokenRunes {
			if len(t.chars) > 0 {
				b := t.chars[0].base
				if dt.shortStarts == nil {
					dt.shortStarts = make(map[rune][]int, 8)
				}
				dt.shortStarts[b] = append(dt.shortStarts[b], i)
				if prevShort >= 'a' && prevShort <= 'z' && b >= 'a' && b <= 'z' {
					dt.bigrams[prevShort-'a'] |= 1 << (b - 'a')
				}
				prevShort = t.chars[len(t.chars)-1].base
			}
			if runStart < 0 {
				runStart = i
			}
			runBuf.WriteString(t.folded)
		} else {
			prevShort = 0
			flushRun(i)
		}
	}
	flushRun(len(tokens))
	return dt
}

func ruleMatchesDoc(rule *CompiledRule, doc *docTokens) bool {
	if !hasAllBigrams(&doc.bigrams, &rule.minBigrams) {
		return false
	}

	// (a) Sequence alignment on token boundaries (1-to-1 per-token E9(a) or merged >=2 short tokens <=2 runes).
	kLen := len(rule.tokens)
	if kLen > 0 && kLen <= len(doc.tokens) && hasAllBigrams(&doc.bigrams, &rule.bigrams) {
		allowUnaccentedDoc := kLen >= 2 && rule.compactRuneLen >= minCompactMatchRunes
		kw0 := &rule.tokens[0]
		limit := len(doc.tokens) - kLen
		for _, pos := range doc.positions[kw0.folded] {
			if pos > limit {
				break
			}
			if tokenMatchesKeyword(kw0, &doc.tokens[pos], rule.compactRuneLen, allowUnaccentedDoc) &&
				alignFrom(rule, doc, 1, pos+1, allowUnaccentedDoc) {
				return true
			}
		}
		if rule.compactRuneLen >= minCompactMatchRunes && len(kw0.chars) >= 2 {
			for _, pos := range doc.shortStarts[kw0.chars[0].base] {
				if pos >= len(doc.tokens)-kLen {
					break
				}
				if alignMergedToken(rule, doc, 0, pos, allowUnaccentedDoc) {
					return true
				}
			}
		}
	}

	// (b) A single token equals the compact (concatenated) keyword of length >= 4.
	if rule.allowCompactSingleToken && doc.hasFolded4 {
		if _, ok := doc.foldedSet[rule.compact]; ok {
			return true
		}
	}

	// (c) Contiguous short tokens (each length <= 2) concatenated match the compact keyword (>= 4)
	//     on token boundaries without contradicting the keyword's Vietnamese vowel shapes or tone set.
	if rule.compactRuneLen >= minCompactMatchRunes && len(doc.shortRuns) > 0 {
		for i := range doc.shortRuns {
			if shortRunMatchesRule(rule, &doc.shortRuns[i]) {
				return true
			}
		}
	}

	return false
}

func alignFrom(rule *CompiledRule, doc *docTokens, kwIdx, pos int, allowUnaccentedDoc bool) bool {
	if kwIdx == len(rule.tokens) {
		return true
	}
	if pos >= len(doc.tokens) {
		return false
	}
	kw := &rule.tokens[kwIdx]
	if tokenMatchesKeyword(kw, &doc.tokens[pos], rule.compactRuneLen, allowUnaccentedDoc) &&
		alignFrom(rule, doc, kwIdx+1, pos+1, allowUnaccentedDoc) {
		return true
	}
	if rule.compactRuneLen >= minCompactMatchRunes && len(kw.chars) >= 2 {
		return alignMergedToken(rule, doc, kwIdx, pos, allowUnaccentedDoc)
	}
	return false
}

func alignMergedToken(rule *CompiledRule, doc *docTokens, kwIdx, pos int, allowUnaccentedDoc bool) bool {
	kw := &rule.tokens[kwIdx]
	targetLen := len(kw.chars)
	if pos >= len(doc.tokens) || doc.tokens[pos].runeLen > maxShortTokenRunes ||
		len(doc.tokens[pos].chars) == 0 || doc.tokens[pos].chars[0].base != kw.chars[0].base {
		return false
	}
	maxRaw := maxMergeRawFactor*targetLen + maxMergeRawPad
	var rawStack, colStack [64]parsedChar
	rawBuf := rawStack[:0]
	for b := pos; b < len(doc.tokens); b++ {
		tok := &doc.tokens[b]
		if tok.runeLen > maxShortTokenRunes || len(rawBuf)+len(tok.chars) > maxRaw {
			return false
		}
		rawBuf = append(rawBuf, tok.chars...)
		if b == pos {
			continue
		}
		seg := collapseParsedChars3Into(rawBuf, colStack[:0])
		checkLen := len(seg)
		if checkLen >= 2 && seg[checkLen-1].base == seg[checkLen-2].base {
			checkLen--
		}
		if checkLen > targetLen {
			return false
		}
		for k := 0; k < checkLen; k++ {
			if seg[k].base != kw.chars[k].base {
				return false
			}
		}
		if len(seg) == targetLen && segmentMatchesChars(seg, kw.chars, kw.toneMask, kw.hasDiacritic) &&
			alignFrom(rule, doc, kwIdx+1, b+1, allowUnaccentedDoc) {
			return true
		}
	}
	return false
}

func addTokenBigrams(m *[26]uint32, chars []parsedChar) {
	for k, c := range chars {
		if c.base < 'a' || c.base > 'z' {
			continue
		}
		m[c.base-'a'] |= 1 << 26
		if k+1 < len(chars) && chars[k+1].base >= 'a' && chars[k+1].base <= 'z' {
			m[c.base-'a'] |= 1 << (chars[k+1].base - 'a')
		}
	}
}

func hasAllBigrams(doc, rule *[26]uint32) bool {
	for i := range rule {
		if rule[i]&^doc[i] != 0 {
			return false
		}
	}
	return true
}

func tokenMatchesKeyword(kw, dt *normToken, compactRuneLen int, allowUnaccentedDoc bool) bool {
	if !kw.hasDiacritic {
		return dt.folded == kw.folded
	}
	if dt.multiTone || kw.multiTone {
		return dt.folded == kw.folded
	}
	if dt.shaped == kw.shaped && dt.toneMask == kw.toneMask {
		return true
	}
	if compactRuneLen >= minCompactMatchRunes && dt.folded == kw.folded {
		if dt.hadLeet || (allowUnaccentedDoc && !dt.hasDiacritic) {
			return true
		}
	}
	return false
}

func shortRunMatchesRule(rule *CompiledRule, run *shortRun) bool {
	if !strings.Contains(run.foldedFlat, rule.compact) {
		return false
	}
	firstBase := rule.compactChars[0].base
	targetLen := rule.compactRuneLen
	var rawStack [64]parsedChar
	var colStack [64]parsedChar
	nTok := len(run.tokens)

	for a := 0; a < nTok; a++ {
		if len(run.tokens[a].chars) == 0 || run.tokens[a].chars[0].base != firstBase {
			continue
		}
		rawBuf := rawStack[:0]
		for b := a; b < nTok; b++ {
			rawBuf = append(rawBuf, run.tokens[b].chars...)
			if len(rawBuf) < targetLen {
				continue
			}
			seg := collapseParsedChars3Into(rawBuf, colStack[:0])
			if len(seg) == targetLen {
				if segmentMatchesChars(seg, rule.compactChars, rule.compactToneMask, rule.hasDiacritic) {
					return true
				}
			} else if len(seg) > targetLen+1 {
				break
			}
		}
	}
	return false
}

func segmentMatchesChars(seg, target []parsedChar, targetToneMask uint8, checkDiacritics bool) bool {
	for k := 0; k < len(seg); k++ {
		if seg[k].base != target[k].base {
			return false
		}
	}
	if !checkDiacritics {
		return true
	}
	var segToneMask uint8
	for k := 0; k < len(seg); k++ {
		c := seg[k]
		if (c.shaped != c.base || c.toneMask != 0) && c.shaped != target[k].shaped {
			return false
		}
		segToneMask |= c.toneMask
	}
	return segToneMask&^targetToneMask == 0
}

// tokenize runs the single-pass E9 normalization pipeline:
// NFKD -> strip Cf -> fold homoglyphs & lowercase -> fold leet -> keep Vietnamese combining marks ONLY on
// Vietnamese vowels producing valid Vietnamese letters (dropping marks on consonants and non-Vietnamese
// precomposed Latin diacritics) -> collapse >=3 same-base consecutive runs preserving vowel shape & tone ->
// split into alphanumeric tokens with tone-position-invariant (shaped, toneMask) representation.
func tokenize(input string, needRaw bool) []normToken {
	if strings.TrimSpace(input) == "" {
		return nil
	}

	parsed := make([]parsedChar, 0, len(input))
	fastOK := true
	for i := 0; i < len(input); {
		if b := input[i]; b < utf8.RuneSelf {
			parsed = append(parsed, asciiParsed[b])
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(input[i:])
		if vc, ok := lookupVietnameseChar(r); ok {
			parsed = append(parsed, vc)
			i += size
			continue
		}
		fastOK = false
		break
	}
	if !fastOK {
		parsed = parsed[:0]
		lastIdx := -1
		for _, r := range norm.NFKD.String(input) {
			if unicode.Is(unicode.Cf, r) {
				continue
			}
			if isCombiningMark(r) {
				if lastIdx >= 0 && isVietnameseVowelBase(parsed[lastIdx].base) {
					if toneBit := vietnameseToneBit(r); toneBit != 0 {
						parsed[lastIdx].toneMask |= toneBit
					} else {
						applyVietnameseQuality(&parsed[lastIdx], r)
					}
				}
				continue
			}
			// Fold homoglyphs BEFORE ToLower because upper/lower Greek confusables differ (e.g. Η->h, η->n).
			lr := unicode.ToLower(foldHomoglyph(r))
			if lr == 'đ' {
				parsed = append(parsed, parsedChar{base: 'd', shaped: 'đ', isAlnum: true})
				lastIdx = len(parsed) - 1
				continue
			}
			fr := foldLeet(lr)
			isAlnum := unicode.IsLetter(fr) || unicode.IsDigit(fr)
			parsed = append(parsed, parsedChar{base: fr, shaped: fr, leet: fr != lr, isAlnum: isAlnum})
			if isAlnum {
				lastIdx = len(parsed) - 1
			} else {
				lastIdx = -1
			}
		}
	}

	if len(parsed) == 0 {
		return nil
	}

	// Collapse >=3 consecutive alphanumeric characters sharing the same base letter in place.
	collapsed := collapseParsedChars3Into(parsed, parsed[:0])

	// Split collapsed characters into alphanumeric tokens.
	tokens := make([]normToken, 0, len(collapsed)/4+1)
	tokStart := -1
	flushTok := func(end int) {
		if tokStart < 0 {
			return
		}
		tokChars := collapsed[tokStart:end]
		tokStart = -1
		if len(tokChars) == 0 {
			return
		}
		var (
			foldedBuf   strings.Builder
			shapedBuf   strings.Builder
			hasShaped   bool
			toneMask    uint8
			hadLeet     bool
			hasConflict bool
		)
		foldedBuf.Grow(len(tokChars))
		for _, c := range tokChars {
			foldedBuf.WriteRune(c.base)
			if c.shaped != c.base {
				hasShaped = true
			}
			toneMask |= c.toneMask
			if c.leet {
				hadLeet = true
			}
			if c.conflict {
				hasConflict = true
			}
		}
		folded := foldedBuf.String()
		shaped := folded
		if hasShaped {
			shapedBuf.Grow(len(tokChars) * 2)
			for _, c := range tokChars {
				shapedBuf.WriteRune(c.shaped)
			}
			shaped = shapedBuf.String()
		}
		multiTone := hasConflict || bits.OnesCount8(toneMask) > 1
		hasDiacritic := toneMask != 0 || hasShaped
		raw := ""
		if needRaw {
			if !hasDiacritic {
				raw = folded
			} else {
				raw = composeRawToken(tokChars)
			}
		}
		tokens = append(tokens, normToken{
			raw:          raw,
			shaped:       shaped,
			folded:       folded,
			chars:        tokChars,
			toneMask:     toneMask,
			multiTone:    multiTone,
			hasDiacritic: hasDiacritic,
			hadLeet:      hadLeet,
			runeLen:      len(tokChars),
		})
	}

	for i, c := range collapsed {
		if c.isAlnum {
			if tokStart < 0 {
				tokStart = i
			}
		} else {
			flushTok(i)
		}
	}
	flushTok(len(collapsed))
	if len(tokens) == 0 {
		return nil
	}
	return tokens
}

// collapseParsedChars3Into collapses any consecutive run of >=3 alphanumeric parsedChars sharing the same
// base letter into a single parsedChar, preserving Vietnamese vowel shape (â ă ê ô ơ ư đ), combining tone
// masks and leet flags, and recording shape conflicts. Runs of length 1 or 2 are preserved intact.
func collapseParsedChars3Into(chars, out []parsedChar) []parsedChar {
	n := len(chars)
	for i := 0; i < n; {
		if !chars[i].isAlnum {
			out = append(out, chars[i])
			i++
			continue
		}
		base := chars[i].base
		j := i + 1
		for j < n && chars[j].isAlnum && chars[j].base == base {
			j++
		}
		if j-i < 3 {
			out = append(out, chars[i:j]...)
		} else {
			merged := parsedChar{
				base:    base,
				shaped:  base,
				isAlnum: true,
			}
			for k := i; k < j; k++ {
				c := chars[k]
				if c.shaped != c.base {
					if merged.shaped == merged.base {
						merged.shaped = c.shaped
					} else if merged.shaped != c.shaped {
						merged.conflict = true
					}
				}
				merged.toneMask |= c.toneMask
				if c.leet {
					merged.leet = true
				}
				if c.conflict {
					merged.conflict = true
				}
			}
			out = append(out, merged)
		}
		i = j
	}
	return out
}

func composeRawToken(chars []parsedChar) string {
	var b strings.Builder
	b.Grow(len(chars) * 3)
	for _, c := range chars {
		b.WriteRune(c.shaped)
		if mark := firstToneMarkRune(c.toneMask); mark != 0 {
			b.WriteRune(mark)
		}
	}
	return norm.NFC.String(b.String())
}

func firstToneMarkRune(mask uint8) rune {
	switch {
	case mask&toneGrave != 0:
		return '\u0300'
	case mask&toneAcute != 0:
		return '\u0301'
	case mask&toneHook != 0:
		return '\u0309'
	case mask&toneTilde != 0:
		return '\u0303'
	case mask&toneDot != 0:
		return '\u0323'
	default:
		return 0
	}
}

func isVietnameseVowelBase(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'y':
		return true
	default:
		return false
	}
}

func applyVietnameseQuality(c *parsedChar, mark rune) {
	if c.shaped != c.base {
		return
	}
	switch mark {
	case '\u0302': // combining circumflex
		switch c.base {
		case 'a':
			c.shaped = 'â'
		case 'e':
			c.shaped = 'ê'
		case 'o':
			c.shaped = 'ô'
		}
	case '\u0306': // combining breve
		if c.base == 'a' {
			c.shaped = 'ă'
		}
	case '\u031B': // combining horn
		switch c.base {
		case 'o':
			c.shaped = 'ơ'
		case 'u':
			c.shaped = 'ư'
		}
	}
}

func vietnameseToneBit(r rune) uint8 {
	switch r {
	case '\u0300':
		return toneGrave
	case '\u0301':
		return toneAcute
	case '\u0309':
		return toneHook
	case '\u0303':
		return toneTilde
	case '\u0323':
		return toneDot
	default:
		return 0
	}
}

func isCombiningMark(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func foldHomoglyph(r rune) rune {
	switch r {
	// Cyrillic visually confusable letters
	case 'А', 'а':
		return 'a'
	case 'В', 'в':
		return 'b'
	case 'С', 'с':
		return 'c'
	case 'Ԁ', 'ԁ':
		return 'd'
	case 'Е', 'е', 'Ё', 'ё', 'Є', 'є':
		return 'e'
	case 'Н', 'н', 'Һ', 'һ':
		return 'h'
	case 'І', 'і', 'Ї', 'ї', 'Ӏ', 'ӏ':
		return 'i'
	case 'Ј', 'ј':
		return 'j'
	case 'К', 'к', 'Қ', 'қ':
		return 'k'
	case 'М', 'м':
		return 'm'
	case 'О', 'о':
		return 'o'
	case 'Р', 'р':
		return 'p'
	case 'Ԛ', 'ԛ':
		return 'q'
	case 'Ѕ', 'ѕ':
		return 's'
	case 'Т', 'т':
		return 't'
	case 'Ѵ', 'ѵ':
		return 'v'
	case 'Ԝ', 'ԝ':
		return 'w'
	case 'Х', 'х':
		return 'x'
	case 'У', 'у', 'Ү', 'ү':
		return 'y'

	// Greek visually confusable letters
	case 'Α', 'α':
		return 'a'
	case 'Β', 'β':
		return 'b'
	case 'Ε', 'ε':
		return 'e'
	case 'Η':
		return 'h'
	case 'η':
		return 'n'
	case 'Ι', 'ι':
		return 'i'
	case 'Κ', 'κ':
		return 'k'
	case 'Μ', 'μ':
		return 'm'
	case 'Ν':
		return 'n'
	case 'ν':
		return 'v'
	case 'Ο', 'ο':
		return 'o'
	case 'Ρ', 'ρ':
		return 'p'
	case 'Σ', 'σ', 'ς':
		return 's'
	case 'Τ', 'τ':
		return 't'
	case 'Υ':
		return 'y'
	case 'υ':
		return 'u'
	case 'Χ', 'χ':
		return 'x'
	case 'Ζ', 'ζ':
		return 'z'
	case 'Ω', 'ω':
		return 'w'

	// Non-decomposing non-Vietnamese Latin letters
	case 'ı':
		return 'i'
	case 'Ł', 'ł':
		return 'l'
	case 'Ø', 'ø':
		return 'o'
	case 'Ħ', 'ħ':
		return 'h'
	case 'Ŧ', 'ŧ':
		return 't'
	case 'Ð', 'ð':
		return 'd'
	default:
		return r
	}
}

func foldLeet(r rune) rune {
	switch r {
	case '0':
		return 'o'
	case '1':
		return 'i'
	case '3':
		return 'e'
	case '4', '@':
		return 'a'
	case '5', '$':
		return 's'
	case '7':
		return 't'
	default:
		return r
	}
}

// collapseRepeats3 collapses any consecutive run of >= 3 identical runes into a single rune,
// while leaving pairs (length 2, e.g. "100" -> "ioo", "free") intact.
func collapseRepeats3(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	var (
		prev   rune
		runLen int
	)
	flush := func() {
		if runLen == 0 {
			return
		}
		if runLen >= 3 {
			b.WriteRune(prev)
		} else {
			for i := 0; i < runLen; i++ {
				b.WriteRune(prev)
			}
		}
	}

	for _, r := range s {
		if runLen == 0 {
			prev = r
			runLen = 1
			continue
		}
		if r == prev {
			runLen++
			continue
		}
		flush()
		prev = r
		runLen = 1
	}
	flush()
	return b.String()
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
