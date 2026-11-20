# DIRECTION SHEET — The contact sheet
seed 98794034 · drawn 2026-11-19 · sector fashion
Brief: NexusMart: sàn thương mại điện tử đa gian hàng Việt Nam, đồ basic nam nữ giá tốt, flash sale, voucher, giao nhanh 34 tỉnh

| | |
| --- | --- |
| Direction | The contact sheet |
| Hero | Ring of frames around the headline |
| Type | Work Sans |
| Colour | Frost and slate |
| Sections | showcase (One image across tiles) → list (Two-ended rows, narrow column) → features (One-size name columns) → gallery (Dense masonry sheet) → proof (Plain names in columns) → faq (Answers as reading text) → footer (A single quiet line) |
| Motion | Masked headline |
| Photographs | the "Desert and road" set, as atmosphere |

This is the direction for this build. Adopt it. Do not blend it with the layout you would have made anyway,
and do not redraw because it is unfamiliar: unfamiliar is the point. Redraw a slot only when it conflicts
with a stated brand constraint or needs an asset that does not exist, and say why in your report.

The parts of this sheet were written separately and will disagree in small ways. Settle every disagreement in this order:
**house rules** (including type no smaller than 13px and body text of 16px or more) → **the direction** (the idea, the composition habits, where the
one accent goes) → **the type system and the palette** (which face does which job, the five size steps, which colour is the ground and where the
accent goes) → **the hero blueprint** → **the section blueprints**. Under each blueprint below, "Adjusted for this page" lists where it disagrees.
In practice: take geometry from blueprints (columns, proportions, what sits where) and take everything else from above them. Map every size a
blueprint names onto one of your five type steps instead of adding a sixth. Use one button shape, one corner radius and one pair of page margins on
the whole page, whatever individual blueprints say. If a blueprint names a kind of typeface (a light serif, a heavy grotesk), use the dealt face.

## 1. The idea
Show everything at once, small, in order. The work is laid out like a photographer's contact sheet or an archive drawer: dozens of small frames in strict rows, with all text set at one small size. Nothing is featured, which is the point: the volume and consistency of the body of work is the statement, and the visitor does the choosing. It suits people and studios whose strength is range and output.

Before anything else, write one sentence in your notes: "On this page, <the subject's own material> becomes <the device>."
Fill the first blank from your inventory of what only this client has (its name and what it means, its street, its numbers, its
constraint), then put the nearest competitor in the sentence: if it still works, the idea is the category's, not the client's.
Then name the one live thing the visitor can do on the page (SKILL.md step 4).

## 2. Composition
The name, or one sentence about the work, sits at display size (6–9vw) across the top of the first screen, and the sheet begins directly under it, still inside the first screen. The first screen opens with three or four short blocks of small text across the top on a 12-column grid (who, what, where to reach them), all at 14–16px, and immediately below, the sheet begins: rows of thumbnails at a fixed height (90–140px) with their natural widths, 6–10px gaps, each project forming a run of four to eight frames preceded by its title in the same small text. The sheet fills the whole page width edge to edge with 16–20px margins. Hovering a run dims the others and shows that project's one-line description in a fixed position. A single enlarged frame may interrupt the sheet once per screen at four times the size.

## 3. Type — Work Sans
A light grotesque with open, slightly old-fashioned shapes; large headings look airy and the text weight beneath stays easy to read. Suits AI and research products, fashion houses and calm brand sites.
```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Work+Sans:wght@300;400;500&display=swap" rel="stylesheet">
```
- Display: **Work Sans** — weight 300 · tracking -0.035em · leading 1.0 · sentence case
- Text: **Work Sans** — 400 for body, 500 for emphasis · leading 1.55 · tracking 0
```css
:root {
  --step-small: clamp(0.8125rem, 0.79rem + 0.1vw, 0.875rem);
  --step-body: clamp(1.0625rem, 1rem + 0.25vw, 1.1875rem);
  --step-lead: clamp(1.25rem, 1.1rem + 0.7vw, 1.625rem);
  --step-head: clamp(2rem, 1.3rem + 3.2vw, 4rem);
}
```
One face. The name at display size on the first screen; everything else at one text size and one small size; hierarchy below the name comes from position and from a single brighter ink for titles.
The direction decides sizes and how many of them; the type system decides how this face is set. Where the two give different tracking or leading, use the values of the type system: they are tuned to this face.
The display size itself comes from the hero blueprint below.

## 4. Colour — Frost and slate
```css
:root {
  --bg: #f7f9fa;
  --surface: #ecf0f2;
  --ink: #1f3038;
  --muted: #5d6b72;
  --accent: #0d7480;
  --accent-ink: #ffffff;
}
```
Cool white and one paler blue-grey step hold everything, with type in dark slate instead of black. Teal appears once per screen at most, as the primary button or one small detail beside the product photograph.
Black or white ground, so the thumbnails are the only colour.

## 5. Imagery
**The project has no photographs of its own yet.** First choice: **real pictures of the subject's world from the open collections**: its city and street in old postcards and photographs, an old map of the neighbourhood, its materials up close, its trade in engravings, museum objects of its kind (references/imagery.md §3). Start with `node /home/dev/.claude/skills/design-taste-frontend/scripts/assets.mjs "fashion"`, then queries from your inventory (its street, its materials, its trade's tools and history). Download what you use into the project with `--get` and credit every one on the page (`--credits` prints the footer line). They show the world around the client, never the client's own room, dish, product, person or team. Second, for what no honest picture exists of: draw the subject's own things in code, large, in one flat graphic language and the palette's real colours (its tiles, its windows, its objects; references/imagery.md §8). Third: the dealt photographs, only where an honestly generic picture fits (the sea, the weather, a material), never one a visitor could take for the thing itself and never an unrelated scene. One treatment for all of them. If this direction is built on photographs, plan the set before building (four to six pictures of one family) and reuse them at different crops and scales instead of finding or drawing a new picture for every slot; keep drawings as `<symbol>` + `<use>` or generate them with a small script so the page stays light. List every found picture (with its source and licence), every dealt photograph and every drawing that stands in for photography in your report.

Many images: at least 24 frames to make a sheet. They can be project photographs from the dealt set cropped to mixed ratios, or page captures of the work. Square corners, no captions on the frames.

**Floor (the inspector checks it):** at least four real pictures on the page, usually six to twelve; SVG drawings do not count. One moving picture where an honest clip exists: `node <skill>/scripts/assets.mjs --video "<query>"`, used as a muted loop with a poster frame. Whatever this direction says about pictures, it is carried out with real ones.

Photographs dealt for this build, from the "Desert and road" set (dunes, dry land and open roads)
Order of preference for imagery: (1) the project's own photographs and product shots; (2) images you generate, if an image tool is
available; (3) real pictures of the subject's world found in the open collections (assets.mjs, downloaded and credited on the page);
(4) drawings of the subject in code; (5) these. They are real photographs (Unsplash licence, served by picsum.photos) of places, light and
texture. They are atmosphere, never a stand-in for a specific product, dish, person or interface. List the ones you use in your report so
they can be replaced.
- `https://picsum.photos/id/314/1600/1000` — Empty two-lane road curving through pale sand dunes · mid tone · sand beige, asphalt grey, teal · calm area: top · native landscape · photo: Fré Sonneveld
- `https://picsum.photos/id/563/1600/1000` — Straight road with a white centre line leading toward dark conical mountains · mid tone · charcoal, olive green, pale grey · calm area: top · native landscape · photo: Bec Brown
- `https://picsum.photos/id/964/1600/1000` — Empty road curving through a snow-covered landscape · light tone · white, asphalt grey, pale blue · calm area: top · native landscape · photo: Jon Ottosson
- `https://picsum.photos/id/261/1600/1000` — Sharp sand dune ridge dividing sunlit and shadowed slopes · mid tone · golden sand, deep indigo · calm area: left · native landscape · photo: Fabio Rose
- `https://picsum.photos/id/525/1600/1000` — Orange sand dune with tiny figures on its ridge under blue sky · mid tone · burnt orange, sky blue · calm area: top · native landscape · photo: Luca Zanon
- `https://picsum.photos/id/379/1600/1000` — Empty asphalt road running through a brown moorland valley under overcast sky · mid tone · warm grey, brown, pale grey · calm area: top · native landscape · photo: Kamil Lehmann
- `https://picsum.photos/id/650/1600/1000` — Road curving past dark pine forest into fog · mid tone · grey, dark green, white · calm area: top · native landscape · photo: Lou Levit
- `https://picsum.photos/id/791/1600/1000` — Hot air balloons drifting over a rocky valley at sunrise · mid tone · golden yellow, amber, dusky purple · calm area: top · native landscape · photo: Daniela Cuevas
The last two path numbers are width and height: request the exact crop you render (`/1200/1500` for a 4:5 portrait, `/2400/1350` for a hero) and
add `?grayscale` to desaturate. Use three to six of them, not all eight. Always set `width`, `height`, `alt` and `loading="lazy"` (not on the hero image).
Other sets that fit this brief: fog-and-quiet, coast, city-day, city-night — `node draw.mjs --photos <set>` prints one in full; `--photos list` shows all sets. Taking photographs from another set is your call and is not a redraw; keep them to one light and one treatment.

## 6. Hero — Ring of frames around the headline
One near-black or deep flat field. Eighteen to twenty-four photographs or film stills sit on an ellipse centred on the viewport, with radii of 560px across and 310px down, so their centres run from x 160 to 1280 and from y 140 to 760. Frames have square corners and no border, mix 4:3 and 3:4, and grow towards the viewer: about 100px wide at the top of the ring and 150px at the bottom and at both ends, so the ring reads as tilted. Each frame is turned so its base lies along the curve (never more than 30 degrees), and neighbours may overlap by up to a fifth. Inside the ring an empty core about 600×340px holds the text, centred: the headline on two lines at clamp(56px, 5.4vw, 80px), leading 0.95, in the light ink; one supporting sentence at 18px, max 40ch, 24px under it; then the single action as a solid light rectangle. Wordmark top-left and three text links top-right at 15px on 32px margins, outside the ring. Every frame is a link to its project: pointing at one, or focusing it, brings it to full brightness, drops the others to 45% and replaces the supporting sentence with that project's name and year. Place the frames with a few lines of trigonometry (left and top in percentages) and keep them still; the wheel scrolls the page as usual.

On a phone: The ellipse becomes a circle 132vw across, centred at half height, so the frames at its left and right ends run off both edges and the ring reads as two arcs, one above and one below the text. Keep twelve frames at 64–88px. The headline sits in the core at 10.5vw on two lines inside 28px margins, with the sentence at 16px and the action at the full width of the core beneath it. Wordmark top-left and a menu word top-right sit above the top arc. A tap on a frame opens its project.
Headline: A 3–6 word line on two lines, at most about 14 characters per line, so it fits the empty core.

Adjusted for this page:
- It fades text: anything a visitor reads, inactive items of a list included, is `--ink` or `--muted` at full strength (contrast 4.5:1 or more). The active item is told apart by `--ink` against `--muted`, weight or the accent, not by fading the rest.
- It names colours (black): map them onto this palette's roles (ground `--bg`, panels `--surface` and the fields, text `--ink`/`--muted`, the accent `--accent` on the primary action only).

## 7. Sections, in order
Each is a composition to rebuild with this project's content. Keep the geometry; change the subject. Re-order or drop a
section when the argument of the page needs it (see references/copy.md), never to fall back to a card grid.

### 1. showcase — One image across tiles (mosaic)
One photograph is cut across a board of tiles. A grid of six columns and four rows with 8px gaps spans the full width between 12px margins; every tile is square (aspect-ratio: 1). Each tile shows its own piece of the same image: set the photograph as every tile's background at background-size: 600% 400% and step background-position by column and row, so the picture reads whole through the gaps. Five of the twenty-four tiles are swapped for flat colour tiles from the palette, never two adjacent: one holds the heading (two to four words at 2.6vw, top-left, 16px padding), three hold one fact each at 18px, one holds the action. On hover a tile's piece scales to 108% inside its own clip. Choose a photograph with one clear subject, so the board is legible at a glance.
On a phone: Three columns by six rows with 6px gaps, the photograph re-cut at background-size 300% 600% from a portrait crop. Four colour tiles only; the heading tile spans two columns in the top row with the heading at 6vw. Facts drop to 15px.

### 2. list — Two-ended rows, narrow column (list)
A list in one narrow column, 520px wide, starting at 22% of the viewport width or centred. Every row is 34–38px tall with no box and nothing drawn between rows. Each row has two ends: at the left, a 16px round image (a face, a logo, a colour dot) followed after 8px by the name at 15–16px in full ink; at the far right of the column, a short value at 13px in the soft ink (a date, a price, a domain, an opening time) with tabular figures. Entries that need a sentence run to two lines at the left, and the value aligns with the first line. Groups are separated by 40px of space and begin with one plain word in the soft ink at the same size. A heading of two to four words at 24–28px in medium weight sits 32px above the first row. Holds 10–40 rows.
On a phone: The column fills the width with 20px margins. Rows are 44px tall for touch; the left image stays 16px. Long values wrap beneath the name at 13px, left-aligned with it, and the row grows. Group spacing is 32px.

### 3. features — One-size name columns (index)
Pure text at one size. Three columns start on columns 1, 4 and 7 of the 12-column grid, leaving columns 10–12 empty on purpose. Each column opens with a group name in full ink and continues with a tight stack of six to ten capability names in the soft ink, all at 16–17px, regular weight, line-height 1.25, no bullets and no symbols; one blank line separates group name from stack. Hovering a name brings it to full ink and shows a one-line description in a fixed spot: the top of the empty columns 10–12, at the same size. The heading is no larger than 28px; two or three words, alone on column 1, 96px above the columns. Give the section 20vh of empty ground above and below, so the smallness reads as decided. Works on light or deep grounds.
On a phone: Columns stack with 40px between groups; text rises to 18px with line-height 1.4 for touch. Group names stay in full ink. Hover descriptions are dropped; each group ends instead with one sentence in the soft ink that sums it up. The heading is 24px, 48px above.

### 4. gallery — Dense masonry sheet (mosaic)
A three-line statement at 22px, 30ch wide, sits at the 66% column with 64px of space below it; it is the only large text. Then a dense masonry of about twenty tiles in six columns with 4px gaps and 12px outer margins, built with CSS `columns: 6; column-gap: 4px` and `break-inside: avoid` on each figure. Tile sizes range from 90px squares to one feature tile of 280 by 160px that sits above the first two columns, outside the column flow. Images keep their natural ratios, so column bottoms are ragged. Every tile has one caption line at 12px directly beneath, 6px below the image and 14px above the next tile, in the form name, comma, type. Mix photographs, posters, details and textures. No hover chrome; the sheet reads at a glance and fills the full width.
On a phone: Three columns with 4px gaps and 10px margins; the feature tile spans the full width at the top. Captions stay at 12px but shorten to the name only. The statement sits above at 19px, full width and left-aligned.

Adjusted for this page:
- It sets text at 12px: use your small step (13px or more) instead.

### 5. proof — Plain names in columns (list)
A typographic list with no logos. The heading, two or three words at 28–32px, sits in columns 1–3 and stays top-aligned with the first row of names. Columns 5–12 hold the names in three or four text columns (`columns: 4; column-gap: 32px`), each name on its own line at 20–22px with leading 1.5, sentence case, in the full ink. Where names fall into kinds (clients, press, partners), each kind starts a new column and its first line names the kind in the softer ink at the same size, so no small labels are needed. Columns are of unequal length and are not balanced; ragged bottoms are intended. Twelve to thirty names work best. There are no boxes, bullets or separators: the column edges and the shared baseline do the organising. Leave 120px above and below.
On a phone: The heading leads at 26px. Names follow in two columns at 17px with a 20px gap; each kind begins after a 32px break with its name in the softer ink on the first line. Long names wrap inside their column rather than shrinking.

### 6. faq — Answers as reading text (grid)
The questions and answers are set as continuous reading text, like the notes at the back of a programme. A heading of two or three words at 28–32px sits in columns 1–3, top-aligned. Columns 5–12 hold the text in two columns (`columns: 2; column-gap: 56px`), each about 36ch wide, at 16–17px with leading 1.55. Each item is one paragraph: the question is the opening run-in, set in the medium weight in the full ink and ending with its question mark, and the answer continues on the same line in the regular weight. Paragraphs are separated by 20px and carry `break-inside: avoid` so none splits across columns. No toggles, no icons, no boxes. Eight to fourteen items fit on one screen, which is the point: everything is answered at a glance. Where the page's reading column is narrow, use one column.
On a phone: One column at 16px with 20px margins. The heading leads at 24px. Each paragraph keeps its run-in question in the medium weight, with 18px between paragraphs. The section may run to two screens; do not collapse it into an accordion.

### 7. footer — A single quiet line (strip)
No footer block at all. After at least 30svh of empty page ground, one baseline 30px above the bottom edge carries three items in the page's ordinary small text size (14–16px), the same size and weight as the navigation: at the left margin, what this is in three or four words; centred, two to five links separated by 24px of space; at the right margin, the year and a privacy link. Use a three-column grid (1fr auto 1fr) so the centre group is truly centred whatever the side lengths. No logo, no background change, no line above it, no columns. Links are in the full ink; the rest in the softer ink. If the page has a narrow text column, the three items may instead sit on that column's left edge as one wrapped line. The restraint only works if the page above has already made its invitation.
On a phone: Three short lines at 14px on the left margin with 8px between them: the description, the links on one wrapped line, then year and privacy. Links stay in the full ink. Leave 20svh of empty ground above and 24px below.

## 8. Motion — Masked headline
On load, each line of the headline rises from behind its own baseline, about 900ms with a strong ease-out, the second line 110ms after the first; the supporting sentence and the action then fade up together. Nothing else on the page performs. Because headlines are two lines at most, you author the line breaks and wrap each line yourself. Recipe: 'Masked line reveal' in references/motion.md.
This is the signature move, the one loud moment. Around it the page still needs the rest of the motion layer (SKILL.md step 9): an entrance for the parts of every section, feedback on every control, and one moving picture. The inspector fails a page that barely moves. Recipes: references/motion.md.

## 10. Where the richness comes from
Density and order. The mass of small images forms a texture; the strict rows keep it calm.

## 11. How this direction goes wrong
With fewer than about 20 images it looks unfinished; choose another direction. Do not add a big headline above the sheet. On a phone the frames become two per row at a fixed height and each run scrolls sideways. Rows that run past the right edge read as clipped in screenshots and to most visitors: wrap the frames onto the next line, or end each row inside the grid.

## 12. Real references
Studied sites that work this way. Take the principle, never the specifics: no names, copy, images or signature assets of theirs.
- **Smallbits** (One Page Love, studied score 7.2) `opl-smallbits`
  Idea: The icon set is the page: a full-viewport wall of the product with one docked card holding every word.
  Hero: The whole viewport is a black field tiled with white pixel icons, each drawn about 50px on a 112px pitch, nine rows deep; alternate rows shift half a cell so it reads as a brick pattern, and icons at the edges are cropped. There is no header. A white square-cornered card, 795×170px, is docked bottom-centre: a two-line 24px semibold sentence left, a black rectangular button right, a row of five grey links below. A third-party badge floats top-right. This single screen is the entire page.
  Worth taking: Make the product the wallpaper: tile the full viewport with the assets at large size and skip a hero image entirely.
- **PROOOOF** (One Page Love, studied score 7.4) `opl-proooof`
  Idea: The product's output is the page: a wall of photos each wearing its coloured EXIF strip, with all the copy folded into one small floating card.
  Hero: The whole viewport is a six-column masonry of photographs on a warm off-white ground with about 15px gutters, columns offset vertically so rows never line up. Each photo has a flat strip fused to its bottom edge carrying tiny monospaced capture data, every strip a different solid colour (black, red, orange, yellow, violet, green). Dead centre floats a white card about 380px wide and 410px tall with roughly 28px corner radius and a soft shadow. It contains an extended wordmark with chained O's, a two-row accordion with the first row open, an App Store badge and a monospaced line of links. There is no navigation.
  Worth taking: Show the product's output at wall scale as the background and confine all copy to one small card on top of it.
- **Decathlon Yestalgia** (Awwwards Site of the Day, studied score 7.8) `aww-decathlon-yestalgia`
  Idea: A full-commitment late-80s Memphis pastiche: sticker shapes with hard black shadows, ultra-condensed capitals and a glitching wordmark that flips nostalgia from no to yes.
  Hero: Pale grey page. A portrait video card, 35% of the width and 75% of the height with square corners, sits dead centre showing a hand on a boombox; across its middle a slanted, extruded gradient wordmark is caught mid-glitch and overhangs the card's left edge. Five flat sticker shapes with speckle patterns and solid black offset shadows are scattered in the empty flanks: pink arc upper-left, lime lightning bolt lower-left, orange disc upper-right, blue arch lower-right, a teal triangle cropped by the top edge. The header is inset 8%: a chunky lime menu button left, the retro logo centred, language selector and shop button right, all with hard black shadows.
  Worth taking: Give every control and sticker a solid black offset shadow instead of a blurred one so flat shapes feel like die-cut objects.
Full notes on any of them: `node draw.mjs --site <id>`.

## Always
Headlines two lines or fewer at every width. No eyebrow labels, no monospace, no 01/02/03, no thin rules, no gradients, no shadows at rest,
no cream-and-terracotta, no violet. The frame: one h1 on the first screen that says what this is, equal outer margins, no
section huddled on one side with the other half empty, no blank stretch over half a screen, a heading on every section.
`inspect.mjs` measures all of it.

Saved to /home/dev/Documents/Nexus_Ecom/.taste-skill/direction.json · reprint with `node draw.mjs --show`
