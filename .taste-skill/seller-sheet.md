# APP SHEET — Achromatic ledger
seed 1009380704 · drawn 2026-11-19 · kind commerce · light
Brief: NexusMart seller center: sản phẩm có biến thể, đơn hàng, khuyến mãi, flash sale, tài chính, chat

This is the style for this product. Adopt it whole and keep its move intact; everything the sheet does not fix comes from the
shared defaults in references/app-screens.md. Redraw only for a reason you can state (it contradicts the brand, or the product is
used in a way the style does not suit) and say why in your report. The values were measured on real product screens: use them
as given rather than rounding them towards your habits.

## 1. The style
The chrome holds no colour at all; money and counts are set in ink, the one chart hue marks the screen's main data element, and the only other hue is a small signal where a figure changed.

**The move:** No brand colour anywhere in the chrome: the one primary action is near-black (a grey pill, #e5e5e5, is the secondary), the main data element (today's series, the booked bars, the current period) takes the one chart hue, #1d5fbf, and everything else is drawn in ink with the comparison or the part after the cursor in grey, zero balances are set in grey, and the only hue on the screen is a small triangle or a short coloured number where a figure changed.

**Colour budget:** under 1% coloured pixels; the chrome is ink, the one chart hue goes on the main data element, and the only other hue is a small change signal.
**Off-style here:** a brand-coloured primary button; tinted cards or tinted KPI tiles; a coloured chart where one series in ink says it; bold on anything but money; a chart with no y-scale (the study's one example of this look hid magnitude behind hover and was flagged); a grey pill as the only button for the screen's main job: in testing an Approve repeated in grey on every row left the screen without a primary.
Distilled from 4 screens: a seller home, a per-agent insights page, a generated report, a settings page with usage numbers. The studied screens of this kind used 11-12px card radii, which §9.4 counts among the rare middle values; the library sets the tight pair (8 and 6) so the style lands on the bimodal rule. After testing, the chart hue moved from an unused third series colour onto the main data element: a screen whose only hue was a delta read as a kit.

**Its face.** Choose one of these (or one of your own of the same weight) before you build, write it in your notes next to
the screen's question, and check that it shows on the first screen. Without one the screen reads as a well-applied kit.
- A ground from the product's world in place of #f8f8f8, one level off white and low in chroma: a stone (#f4f3f1) for a bakery or a shop, a sage (#f2f5f2) for a garden or a greenhouse, a blue-grey (#f2f4f7) for a ledger or a meter. Cards stay white; a warm tint stays under the cream check (saturation under about 15%).
- The one chart hue (#1d5fbf, or the brand's own hue at 3:1 or more on white) on the screen's main data element: today's line, the booked bars, the current period's column, the margin that breaks. The chrome stays ink and the hue stays under 1% of the screen.
- Every figure in Geist Mono, as the style's decision, so money and counts read as a ledger's columns while the text stays Geist. Lint and the inspector flag mono; give the reason in the report.

## 2. Tokens
Kept from the brand: map its colours onto this style's roles (ground, surface, raised, border, rule, control, text, muted, accent, status pills, chart) and keep its proportions: under 1% coloured pixels; the chrome is ink, the one chart hue goes on the main data element, and the only other hue is a small change signal. Take the sizes, radii, row and control heights from the style as they are.

## 4. Shell — Open plane
The labelled sidebar sits on the page ground with nothing or one hairline between it and the canvas; there is no top bar, and the page header and cards sit straight on the ground about 32-36px from the top. Workspace or account switcher at the top of the sidebar, primary nav on a 34px pitch, groups under 12-13px sentence-case grey labels with 22-56px of air above, utilities and the user row pushed to the bottom. Cards are white on the off-white ground (or the sidebar is white with a 1px border on a #f9fafb canvas). The canvas is fluid with one gutter value, or a centred column for personal homes.
Numbers: sidebarWidth 230-256 (230, 240, 256 in the study; 280 and 292 with a built-in icon rail); separation none, or one 1px hairline; the sidebar sits on the page ground or within 1-2% of it; navPitch 34; navRowHeight 32; navIcon 16px outline, about 1.5px stroke; navLabel 14px, 400, grey or ink; activeItem neutral grey pill, radius 6-12, inset 8-12px from the sidebar edges (#e9e9e9-#ededed); topBar none; canvas fluid with a 24-64px gutter, or a centred 1100px column.
On a phone: The sidebar folds into a 48px top bar holding the screen's name, a menu button that opens a full-screen sheet with the nav and the switcher, and at most one action. Content takes the full width with 16px margins.

## 5. Screens

### overview — KPIs as the chart's tabs
One card per subject, 8-16px apart: the main chart card runs full width, and any card holding a table of five columns or fewer, or a short list, pairs with its neighbour in two columns, the page header indented by the card padding so the title, the card titles, the numbers and the plot share one left edge. Balances that do not change during the day go first, in one container split by 1px dividers into equal columns; a zero balance is grey and its caption becomes the next step. In the main card, 2-4 KPIs sit in a row and act as the chart's series tabs: the selected one carries a 2px rule and an ink label, the others keep a grey label and their value in ink at a lighter weight (a grey value reads as disabled); when the tabs are the per-entity breakdown the brief asks for (takings by shop), nothing is greyed and the rule alone marks the selection. Choosing one swaps the series. Under them, one line chart as wide as the card's content (about 1136x220 in the study): 3-4 solid 1px gridlines, 4-6 round y ticks at 12px, a 2-3px line in ink with straight segments and no fill, an 8px hover dot with a 1px dashed guide and the line after the cursor in pale grey. Close with a 2-3 row comparison table on a 36px pitch, value and change columns right-aligned under a 14px grey header such as 'vs prior Monday'.
On a phone: KPI tabs become a 2-up grid above the chart, with the selected cell marked by a fill (never an underline floating inside the grid) or a row of tabs that scrolls sideways; the chart is full width, 160-200px tall, with 4 x labels; the comparison table keeps its two right-aligned columns.

### list — Count cards that filter
Above a list, 3-5 equal count cards (61px tall, 12px padding, a 13px grey label over a 16/600 value, 8px gap) are the type filter: the selected card gets a 1.5px accent border and a pale accent fill, and the list below shows that type only. Zero counts print as '0' in the same style; no deltas, no icons. This is one place where a row of identical small cards is right: the cards are peers and each one does work.
On a phone: The count cards become a horizontally scrolling row of chips with the count inside each, or a select above the list.

### detail — Object header and a six-column grid
The page header is the object itself: one card (88px, 14px padding) holding a 50x62 cover, the 18/600 name with 28px icon buttons beside it (open, copy, share), a 13px grey meta line (date, time, place) and outlined 36px actions on the right. Under it, underline tabs on a full-width 1px rule, then a 36px row with a segmented time range on the left and a refresh button on the right. The body is one 6-column grid with one gutter (14px) in both directions whose spans change by row while every edge lands on the same lines: 3 KPI tiles of 2 columns, the main chart in 4 columns beside a side chart in 2, two progress cards of 3, a full-width trend of 6. Give the widest card to the screen's question, and do not give 824px to one ring and one legend row (flagged).
On a phone: The header card keeps cover, name and meta, with actions in a kebab; every grid item takes the full width in the same order.

## 6. Always
Real data for this product, written before the markup: names, amounts that add up, dates in order, units on every figure,
honest zeros and dashes. One chart answers one question. No gradients, glows, glass, icons in tinted squares, sparklines on KPIs,
rainbow charts, emoji, lorem or Acme. `<skill>/assets/app.css` and `<skill>/assets/charts.js` are a neutral start set to these
rules. Check with `node <skill>/scripts/lint.mjs <files>` and `node <skill>/scripts/inspect.mjs <page>`: this sheet switches both
to app rules.

Saved to /home/dev/Documents/Nexus_Ecom/.taste-skill/seller/direction.json · reprint with `node draw.mjs --show`
