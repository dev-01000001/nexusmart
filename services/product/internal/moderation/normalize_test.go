package moderation

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTerm(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{"simple_accented", "Hàng   Giả!", "hàng giả", true},
		{"simple_unaccented", "  CAN   SA  ", "can sa", true},
		{"nfkc_fullwidth", "ｈàｎｇ　ｇｉả", "hàng giả", true},
		{"zero_width_cf", "h\u200bàng g\u200ciả\u200d", "hàng giả", true},
		{"cyrillic_homoglyph", "МA TÚY", "ma túy", true},
		{"leet_folded", "f@k3", "fake", true},
		{"repeat_collapsed", "haaaang giiiả", "hang giả", true},
		{"percent_phrase", "Cam kết 100%", "cam kết ioo", true},
		{"two_char_ok", "đá", "đá", true},
		{"empty_rejected", "   ", "", false},
		{"punct_only_rejected", "!!!---...", "", false},
		{"single_char_rejected", "a", "", false},
		{"single_viet_char_rejected", "đ", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NormalizeTerm(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("NormalizeTerm(%q) ok=%v, want %v (got=%q)", tc.input, ok, tc.wantOK, got)
			}
			if got != tc.want {
				t.Fatalf("NormalizeTerm(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestMatch_EvasionCasesBlocked(t *testing.T) {
	cases := []struct {
		name    string
		keyword string
		text    string
	}{
		// Basic token boundary (Rule a)
		{"exact_accented", "hàng giả", "Shop bán hàng giả giá rẻ"},
		{"case_and_spaces", "hàng giả", "HÀNG    GIẢ loại 1"},
		// E9-bis: multi-token accented keyword (>=2 tokens, compact >=4) matches unaccented document tokens
		{"e9bis_unaccented_doc_ma_tuy", "ma túy", "Bán ma tuy giá rẻ"},
		{"e9bis_unaccented_doc_hang_gia", "hàng giả", "hang gia sieu cap"},
		{"e9bis_unaccented_doc_can_sa", "cần sa", "can sa"},
		{"unaccented_kw_matches_accented_text", "hang gia", "Bán hàng giả siêu cấp"},
		{"unaccented_kw_matches_leet", "hang gia", "Bán h@ng gi4 siêu cấp"},
		{"unaccented_kw_matches_zwsp", "hang gia", "h\u200bang g\u200bia chất lượng"},
		{"accented_kw_repeat_chars", "hàng giả", "HÀNG   GIIIẢ"},
		{"accented_kw_repeat_accented", "hàng giả", "hàààng giảảả"},
		{"accented_kw_zwsp_inside", "hàng giả", "h\u200bàng g\u200biả"},
		{"accented_kw_soft_hyphen_and_bom", "hàng giả", "\ufeffh\u00adàng g\u2060iả"},
		{"accented_kw_nfd_with_zwsp", "hàng giả", "ha\u200b\u0300ng gia\u200c\u0309"},
		{"accented_kw_zalgo_strikethrough", "hàng giả", "h\u0336à\u0336n\u0336g g\u0336i\u0336ả\u0336"},
		{"accented_kw_zalgo_before_viet_tone", "hàng giả", "ha\u0336\u0300ng gia\u0308\u0309"},
		{"nfkc_math_bold", "ma túy", "𝐛𝐚́𝐧 𝐦𝐚 𝐭𝐮́𝐲"},
		{"nfkc_fullwidth", "fake", "ｆａｋｅ bag"},
		{"cyrillic_homoglyph_upper", "ma túy", "МА TÚY"},
		{"cyrillic_homoglyph_with_viet_tone", "hàng giả", "h\u0430\u0300ng g\u0456ả"},
		{"greek_homoglyph_upper", "hàng giả", "\u0397ÀNG G\u0399Ả"},
		{"greek_homoglyph_lower", "fake", "f\u03b1k\u03b5"},
		{"leet_all_substitutions", "fake", "f@k3"},
		{"leet_accented_hang_gia", "hàng giả", "Bán h@ng gi4 siêu cấp"},
		{"leet_accented_can_sa", "cần sa", "c@n $4"},
		{"leet_accented_sung", "súng", "Bán $ung đồ chơi"},
		{"leet_digits_and_dollar", "can sa", "c@n $4"},
		{"leet_013457", "toisea", "701534"},
		{"repeat_3_or_more", "fake", "faaaake"},
		{"percent_phrase", "cam kết 100%", "Sản phẩm cam kết 100% khỏi bệnh"},
		{"percent_phrase_caps", "cam kết 100%", "CAM KẾT 100% trị dứt điểm"},

		// Compact single token (Rule b, len >= 4)
		{"compact_single_token_multiword", "hàng giả", "hanggia"},
		{"compact_single_token_leet", "hàng giả", "h@nggi4"},
		{"compact_single_token_cansa", "cần sa", "c@n$a"},
		{"compact_single_token_matuy", "ma túy", "m4tuy"},

		// Short tokens <= 2 concatenated (Rule c, compact len >= 4)
		{"short_tokens_dots_matuy", "ma túy", "m.a t.u.y"},
		{"short_tokens_spaces_matuy", "ma túy", "mua m a t u y rẻ"},
		{"short_tokens_pairs_matuy", "ma túy", "ma tu y"},
		{"short_tokens_dots_hanggia_accented", "hàng giả", "h.à.n.g g.i.ả"},
		{"short_tokens_dots_hanggia_unaccented", "hàng giả", "h.a.n.g g.i.a"},
		{"short_tokens_dots_cansa", "cần sa", "c.ầ.n s.a"},
		{"short_tokens_dots_fake", "fake", "f.a.k.e"},
		{"short_tokens_dots_sung", "súng", "s.ú.n.g"},
		{"short_tokens_dots_with_repeats", "fake", "f.a.a.a.k.e"},

		// RevB P1-3: combining marks on consonants & non-Vietnamese precomposed Latin letters
		{"revb_p1_3_combining_on_t", "ma túy", "bán ma t\u0323úy giá rẻ"},
		{"revb_p1_3_combining_on_g", "hàng giả", "hàn\u0067\u0306 giả"},
		{"revb_p1_3_combining_on_n", "cần sa", "cầ\u006e\u0323 sa"},
		{"revb_p1_3_combining_on_s", "súng", "s\u0323úng"},
		{"revb_p1_3_combining_on_k", "vũ khí", "vũ k\u0323hí"},
		{"revb_p1_3_combining_unaccented_e9bis", "hàng giả", "h\u0323ang g\u0306ia loai 1"},
		{"revb_p1_3_precomposed_s_dot", "súng", "\u1e63úng hơi"},
		{"revb_p1_3_precomposed_t_dot", "ma túy", "ma \u1e6dúy"},
		{"revb_p1_3_precomposed_h_dot", "hàng giả", "\u1e25àng giả"},
		{"revb_p1_3_precomposed_g_breve", "hàng giả", "hàn\u011f giả"},
		{"revb_p1_3_precomposed_m_acute", "ma túy", "\u1e3fa túy"},
		{"revb_p1_3_precomposed_m_acute_accented_tok", "mã độc", "\u1e3fã độc"},

		// RevB P2-4: >=3 repeated same-base vowels mixing accented + unaccented, and multi-tone invalid tokens
		{"revb_p2_4_suuung_lower", "súng", "suúung"},
		{"revb_p2_4_suuung_upper", "súng", "Súuung"},
		{"revb_p2_4_tuuuy", "ma túy", "bán ma túuuy rẻ"},
		{"revb_p2_4_haaang", "hàng giả", "hàaang giả"},
		{"revb_p2_4_caaaan", "cần sa", "cầaaan sa"},
		{"revb_p2_4_thuoooc", "thuốc lắc", "thuooốc lắc"},
		{"revb_p2_4_thuooc_circumflex", "thuốc lắc", "thuôốốc lắc"},
		{"revb_p2_4_multitone_collapsed_repeat", "súng", "sùúung"},
		{"revb_p2_4_multitone_two_vowels", "ma túy", "bán ma tùý"},

		// RevB P3-8: old vs new Vietnamese tone placement
		{"revb_p3_8_ma_tuy_old_new", "ma túy", "bán ma tuý giá rẻ"},
		{"revb_p3_8_hoa_old_new", "hòa bình", "hoà bình"},
		{"revb_p3_8_hoa_new_old", "hoà bình", "hòa bình"},

		// RevB P2-5: valid spaced/dotted evasions that MUST still match shortRuns
		{"revb_p2_5_spaced_accented", "ma túy", "m a t ú y"},
		{"revb_p2_5_dotted_accented", "ma túy", "m.a t.ú.y"},
		{"revb_p2_5_pairs_unaccented", "ma túy", "ma tu y"},
		{"revb_p2_5_leet_spaced", "ma túy", "m4 t u y"},

		// r9: partial split-token evasion mixed with normal tokens
		{"r9_hang_cam_xk_joined_dots", "hàng cấm xk", "h.a.n.g.c.a.m xk"},
		{"r9_hang_cam_xk_spaced_dots", "hàng cấm xk", "h.a.n.g c.a.m xk"},
		{"r9_thuoc_cam_abcdef_mixed_leet", "thuốc cấm abcdef", "Hàng th.u.0.c c.a.m abcdef"},
		{"r9_ma_tuy_partial_accented", "ma túy", "m.a túy"},
		{"r9_ma_tuy_partial_unaccented", "ma túy", "m.a tuy"},
		{"r9_thuoc_lac_split_first", "thuốc lắc", "t.h.u.o.c lắc"},
		{"r9_thuoc_lac_split_second", "thuốc lắc", "thuốc l.ắ.c"},
		{"r9_can_sa_split_first", "cần sa", "c.ầ.n sa"},
		{"r9_can_sa_split_second", "cần sa", "cần s.a"},
		{"r9_vu_khi_split_second", "vũ khí", "vũ kh.í"},

		// r10: restore E9(a) per-token rule (unaccented keyword token "ma" matches folded doc token)
		{"r10_ma_tuy_unaccented_kw_token_grave", "ma túy", "mà túy"},
		{"r10_ma_tuy_unaccented_kw_token_tilde", "ma túy", "mã túy"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Match([]Rule{{ID: "r1", Term: tc.keyword, Action: ActionBlock}}, tc.text)
			if !res.Blocked || len(res.BlockedTerms) != 1 || res.BlockedTerms[0] != tc.keyword {
				t.Fatalf("Match(keyword=%q, text=%q) = %+v, want Blocked=true matching %q", tc.keyword, tc.text, res, tc.keyword)
			}
		})
	}
}

func TestMatch_LegitimateTextNotBlocked(t *testing.T) {
	cases := []struct {
		name    string
		keyword string
		text    string
	}{
		// E9 explicit non-blocking case
		{"can_san_vs_can_sa", "cần sa", "Căn sàn gỗ cao cấp 3 lớp"},
		{"can_sa_different_diacritic", "cần sa", "Căn sa bàn kiến trúc"},
		{"can_san_pham_token_boundary", "cần sa", "Khách cần sản phẩm chất lượng"},
		{"can_sang_token_boundary", "cần sa", "Cần sang nhượng cửa hàng"},
		{"can_nhua_vs_can_sa", "cần sa", "Can nhựa đựng nước 20 lít"},

		// Single accented token must NOT match different diacritic or unaccented common words
		{"bo_sung_vs_sung", "súng", "Thực phẩm bổ sung vitamin C"},
		{"ao_da_vs_da", "đá", "Áo da thật màu đen"},
		{"vai_co_gian_vs_co", "cỏ", "Quần jean vải co giãn 4 chiều"},
		{"ban_lam_viec_vs_ban", "bắn", "Bàn làm việc gỗ sồi tự nhiên"},
		{"ban_hang_vs_ban", "bắn", "Bán hàng chính hãng bảo hành 12 tháng"},

		// Substring inside longer token must NOT match
		{"breakfast_vs_fake", "fake", "Set đồ ăn sáng breakfast kiểu Âu"},
		{"cansan_concat_vs_cansa", "cần sa", "cănsàn"},

		// Different Vietnamese diacritics on multi-word accented keyword
		{"hang_gia_re_vs_hang_gia", "hàng giả", "Sản phẩm hãng giá rẻ bất ngờ"},
		{"ma_tuy_bien_vs_ma_tuy", "ma túy", "Giao diện ma tùy biến linh hoạt"},

		// Double-letter preservation (collapse is >= 3, NOT >= 2)
		{"double_zero_vs_triple_zero", "cam kết 100%", "Cam kết 1000 ngày đổi trả"},

		// Short keyword < 4 compact length must NOT trigger compact (b)/(c) or leet accent bypass
		{"short_keyword_no_compact_match", "ab", "a.b"},
		{"short_keyword_no_substring", "ab", "cabc"},
		{"short_accented_no_leet_false_positive", "đá", "Thực phẩm chức năng Vitamin D4"},

		// Unrelated normal Vietnamese with many short tokens
		{"normal_short_tokens", "ma túy", "Áo sơ mi ở Hà Nội có ba cỡ"},

		// RevB P2-5: shortRuns must respect token boundaries & must not contradict Vietnamese diacritics/tones
		{"revb_p2_5_ma_tu_y_te", "ma túy", "Bán mã tủ y tế gia đình inox 304"},
		{"revb_p2_5_ma_tu_y_policy", "ma túy", "Khách không được tự ý hủy mà tự ý đổi trả"},
		{"revb_p2_5_ma_tu_y_short", "ma túy", "mạ tủ y"},
		{"revb_p2_5_ca_do_huyet", "cá độ", "Cà đo huyết áp điện tử"},
		{"revb_p2_5_non_token_boundary_short_run", "fake", "xf ak ey"},

		// r9/r10: partial split must NOT contradict diacritics/tones on accented kw tokens or trigger on compact < 4
		{"r9_thuoc_lac_wrong_vowel_shape_split", "thuốc lắc", "thuốc l.á c"},
		{"r9_hang_gia_wrong_tone_split_space", "hàng giả", "hàng gi á"},
		{"r9_hang_gia_wrong_tone_first_token", "hàng giả", "hãng g.i.ả"},
		{"r9_hang_gia_wrong_tone_split_dots", "hàng giả", "hàng g.i.á"},
		{"r9_can_sa_wrong_shape_first_token", "cần sa", "căn s.a"},
		{"r9_short_compact_under_4_no_merge", "ab c", "a.b c"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Match([]Rule{{ID: "r1", Term: tc.keyword, Action: ActionBlock}}, tc.text)
			if res.Blocked || res.Flagged || len(res.MatchedTerms) != 0 {
				t.Fatalf("Match(keyword=%q, text=%q) = %+v, want NO match", tc.keyword, tc.text, res)
			}
		})
	}
}

func TestMatchFields_NoCrossFieldBleedAndPrecedence(t *testing.T) {
	rules := []Rule{
		{ID: "r-block", Term: "ma túy", Action: ActionBlock},
		{ID: "r-review", Term: "cam kết 100%", Action: ActionReview},
		{ID: "r-review-2", Term: "chữa bách bệnh", Action: ActionReview},
	}

	// Cross-field boundary must NOT stitch "m" at end of name with "a t u y" at start of description.
	noBleed := Match(rules, "Áo size m", "a t u y chọn màu sắc")
	if noBleed.Blocked || noBleed.Flagged {
		t.Fatalf("unexpected cross-field bleed match: %+v", noBleed)
	}

	// Both BLOCK and REVIEW present across separate fields -> HighestAction=BLOCK, both lists populated sorted.
	both := Match(rules, "Thuốc chữa bách bệnh", "Cam kết 100% không chứa m.a t.u.y")
	if !both.Blocked || !both.Flagged || both.HighestAction != ActionBlock {
		t.Fatalf("expected Blocked+Flagged with HighestAction=BLOCK, got %+v", both)
	}
	if !reflect.DeepEqual(both.BlockedTerms, []string{"ma túy"}) {
		t.Fatalf("BlockedTerms = %v, want [ma túy]", both.BlockedTerms)
	}
	if !reflect.DeepEqual(both.FlaggedTerms, []string{"cam kết 100%", "chữa bách bệnh"}) {
		t.Fatalf("FlaggedTerms = %v, want [cam kết 100%% chữa bách bệnh]", both.FlaggedTerms)
	}

	// REVIEW only -> Blocked=false, Flagged=true, HighestAction=REVIEW.
	revOnly := Match(rules, "Trà thảo mộc", "Cam kết 100% nguyên chất")
	if revOnly.Blocked || !revOnly.Flagged || revOnly.HighestAction != ActionReview {
		t.Fatalf("expected Flagged only with HighestAction=REVIEW, got %+v", revOnly)
	}
}

var benchMatchResult MatchResult

func BenchmarkMatchCompiled_500Rules_10KChars(b *testing.B) {
	rules := make([]Rule, 0, 500)
	seedTerms := []string{"ma túy", "hàng giả", "cần sa", "thuốc lắc", "cá độ", "vũ khí", "súng", "cam kết 100%", "chữa bách bệnh", "nhập lậu"}
	for i := 0; i < 500; i++ {
		act := ActionBlock
		if i%3 == 0 {
			act = ActionReview
		}
		term := fmt.Sprintf("%s mẫu %d", seedTerms[i%len(seedTerms)], i)
		if i < len(seedTerms) {
			term = seedTerms[i]
		}
		rules = append(rules, Rule{ID: fmt.Sprintf("r-%d", i), Term: term, Action: act})
	}
	compiled := CompileRules(rules)

	para := "Sản phẩm hãng giá rẻ bất ngờ với giao diện ma tùy biến linh hoạt, căn sa bàn kiến trúc đẹp mắt, thực phẩm bổ sung vitamin C, bán mã tủ y tế gia đình inox 304, cà đo huyết áp điện tử chính hãng bảo hành 12 tháng. "
	var sb strings.Builder
	for sb.Len() < 10000 {
		sb.WriteString(para)
	}
	desc := sb.String()[:10000]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchMatchResult = MatchCompiled(compiled, desc)
	}
	b.StopTimer()
	if benchMatchResult.Blocked || benchMatchResult.Flagged {
		b.Fatalf("unexpected match on clean 10k Vietnamese description: %+v", benchMatchResult)
	}
}

func buildAdversarial1000Rules() []CompiledRule {
	rules := make([]Rule, 0, 1000)
	// Mix realistic Vietnamese rules AND adversarial a/t/u-only multi-token rules that stress backtracking.
	baseTerms := []string{
		"hàng cấm xk", "thuốc cấm abcdef", "ma túy", "hàng giả", "cần sa",
		"thuốc lắc", "vũ khí", "cam kết 100%", "chữa bách bệnh", "nhập lậu",
	}
	adversarial := []string{
		"tutu tuut", "tut utut", "tuut tuut", "tutu tut uu", "tutu tutu tuut",
		"tu tu tu tu uu", "aa aa aa aa b", "aaaa aaaa b", "atut tuta", "tuta atut",
	}
	for i := 0; i < 1000; i++ {
		var term string
		switch {
		case i < len(baseTerms):
			term = baseTerms[i]
		case i%2 == 0:
			// Adversarial {t,u}-only or {a}-heavy rules (guaranteed not to match pure "a a..." / "t u...").
			base := adversarial[i%len(adversarial)]
			if i%4 == 0 {
				term = fmt.Sprintf("tutu tuut %s", strings.Repeat("tu", (i%3)+1)+"u")
			} else {
				term = fmt.Sprintf("%s %d", base, i)
			}
		default:
			term = fmt.Sprintf("%s mẫu %d", baseTerms[i%len(baseTerms)], i)
		}
		rules = append(rules, Rule{ID: fmt.Sprintf("adv-%d", i), Term: term, Action: ActionBlock})
	}
	return CompileRules(rules)
}

func TestMatchCompiled_Performance_1000Rules_20KRepeatedShortTokens(t *testing.T) {
	compiled := buildAdversarial1000Rules()
	descA := strings.Repeat("a ", 10000)   // 20,000 chars, 10,000 single-char "a" tokens
	descTU := strings.Repeat("t u ", 5000) // 20,000 chars, 10,000 alternating "t","u" tokens

	startA := time.Now()
	resA := MatchCompiled(compiled, descA)
	durA := time.Since(startA)
	if resA.Blocked || resA.Flagged {
		t.Fatalf("unexpected match on repeated 'a ' desc: %+v", resA)
	}

	startTU := time.Now()
	resTU := MatchCompiled(compiled, descTU)
	durTU := time.Since(startTU)
	if resTU.Blocked || resTU.Flagged {
		t.Fatalf("unexpected match on repeated 't u ' desc: %+v", resTU)
	}

	t.Logf("1000 rules x 20k chars: 'a a...'=%v, 't u...'=%v, total=%v", durA, durTU, durA+durTU)
	if durA+durTU >= 2*time.Second {
		t.Fatalf("backtracking blew up: 'a a...'=%v, 't u...'=%v (want < 2s)", durA, durTU)
	}
}

func BenchmarkMatchCompiled_1000Rules_20KRepeatedShortTokens(b *testing.B) {
	compiled := buildAdversarial1000Rules()
	descA := strings.Repeat("a ", 10000)
	descTU := strings.Repeat("t u ", 5000)

	b.Run("repeat_a_20k", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchMatchResult = MatchCompiled(compiled, descA)
		}
	})
	b.Run("repeat_tu_20k", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchMatchResult = MatchCompiled(compiled, descTU)
		}
	})
}

func TestMatch_RandomPartialSplitSubsetsAlwaysMatch(t *testing.T) {
	rng := rand.New(rand.NewSource(20261124))
	keywords := []string{
		"hàng cấm xk",
		"thuốc cấm abcdef",
		"ma túy",
		"hàng giả",
		"cần sa",
		"thuốc lắc",
		"vũ khí",
		"cam kết 100%",
		"chữa bách bệnh",
	}

	for _, kw := range keywords {
		compiled := CompileRules([]Rule{{ID: "r1", Term: kw, Action: ActionBlock}})
		words := strings.Fields(kw)
		for iter := 0; iter < 40; iter++ {
			// Pick a non-empty random subset of tokens to dot-split into <=2-rune chunks.
			mask := rng.Intn((1<<len(words))-1) + 1
			parts := make([]string, len(words))
			for wi, w := range words {
				if (mask>>wi)&1 == 0 {
					parts[wi] = w
					continue
				}
				runes := []rune(w)
				if len(runes) <= 1 {
					parts[wi] = w
					continue
				}
				// Partition runes into chunks of length 1..2 (ensuring at least 2 chunks) joined by ".".
				var chunks []string
				for pos := 0; pos < len(runes); {
					rem := len(runes) - pos
					step := 1
					if rem > 2 && rng.Intn(2) == 1 {
						step = 2
					}
					chunks = append(chunks, string(runes[pos:pos+step]))
					pos += step
				}
				parts[wi] = strings.Join(chunks, ".")
			}
			// Randomly join adjacent split tokens with "." or " ".
			var sb strings.Builder
			if rng.Intn(2) == 1 {
				sb.WriteString("Sản phẩm mới ")
			}
			for pi, p := range parts {
				if pi > 0 {
					bothSplit := ((mask>>(pi-1))&1 == 1) && ((mask>>pi)&1 == 1)
					if bothSplit && rng.Intn(2) == 1 {
						sb.WriteByte('.')
					} else {
						sb.WriteByte(' ')
					}
				}
				sb.WriteString(p)
			}
			if rng.Intn(2) == 1 {
				sb.WriteString(" giao nhanh")
			}
			text := sb.String()
			res := MatchCompiled(compiled, text)
			if !res.Blocked || len(res.BlockedTerms) != 1 || res.BlockedTerms[0] != kw {
				t.Fatalf("keyword=%q iter=%d mask=%b text=%q -> got %+v, want Blocked=true", kw, iter, mask, text, res)
			}
		}
	}
}
