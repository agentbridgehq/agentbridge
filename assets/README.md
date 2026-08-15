# Brand assets

The mark is the letter **A** whose crossbar is a bridge arch — the thing this
project does, in one glyph.

| File | What it is | Use on |
|---|---|---|
| `logo-light.png` | Full lockup: mark + wordmark | **Light** backgrounds |
| `logo-dark.png` | The same lockup in near-white | **Dark** backgrounds |
| `icon-light.png` | Mark only, square, 512px | **Light** backgrounds |
| `icon-dark.png` | Mark only, square, 512px | **Dark** backgrounds |
| `icon-128.png` | Mark only, 128px | Favicons, package listings |

**The `-light` / `-dark` suffix names the background it goes on, not the colour
of the ink.** `logo-dark.png` is the pale one. This is the naming everyone
argues about, so it is written down.

Every file is ink on a **transparent** background, so none of them paints a
white or black box onto whatever it sits on. That matters: the original mark
shipped with a solid white background baked in, which is invisible on a light
page and a bright rectangle on a dark one.

Use `<picture>` so the right one is chosen automatically — GitHub, and anything
else that honours `prefers-color-scheme`, will switch:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="assets/logo-light.png">
  <img alt="AgentBridge" src="assets/logo-light.png" width="360">
</picture>
```

The `src` on the `<img>` is the light variant deliberately: it is the fallback
for anything that ignores the `<source>` elements, and a dark-ink logo on an
unknown background is the safer failure.

## Regenerating

These are derived from two source renders, not hand-drawn. Both derivations are
mechanical — trim to the ink, key out the white background using darkness as
alpha so antialiased edges survive, recolour, and scale — so re-running on a new
source render reproduces the whole set.

Ink is `#111111`; the pale variant is `#FAFAFA`. Neither is pure black or pure
white, which sit badly against real page backgrounds.
