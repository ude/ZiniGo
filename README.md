# ZiniGo

A tool written in Go for saving (legally purchased) magazines from Zinio as DRM-free PDFs.

This repository is [ude/ZiniGo](https://github.com/ude/ZiniGo), a fork of
[ccasalicchio/ZiniGo](https://github.com/ccasalicchio/ZiniGo) (which rewrote the tool for
Zinio's current BFF API), itself a fork of the original project
[TheAxeDude/ZiniGo](https://github.com/TheAxeDude/ZiniGo).

## What this fork changes

- **PDF engine replaced**: pdfcpu was dropped in favor of **PDFium** (Chrome's PDF engine,
  embedded as WebAssembly via [go-pdfium](https://github.com/klippa-app/go-pdfium) — still a
  single self-contained Go binary, no CGO and no external dependencies). pdfcpu corrupted
  many Zinio page PDFs: it stripped font objects referenced from Form XObjects (leaving
  invisible text), dropped optional-content groups, ignored inline `/Encrypt` dictionaries,
  and rejected valid AESV2 passwords on PDF 1.4 files.
- **Page-number annotations removed**: Zinio embeds a `page=N` sticky-note annotation on
  every page; they are now stripped from the final PDF.
- **Reliability**: page downloads retry on transient CDN failures (503s), sessions
  re-authenticate automatically on 401, and failing pages are kept on disk for inspection
  instead of producing a broken magazine.
- **Type 1 font conversion script** for e-ink readers (see below).

## Usage

```
./ZiniGo -u=Username -p=Password
```

Credentials can also be placed in a `config.json` in the working directory:

```json
{
  "username": "you@example.com",
  "password": "your-password"
}
```

A device `fingerprint` is generated and stored in the config on first run.

Flags:

| Flag | Description |
|------|-------------|
| `-u`, `-p` | Zinio username / password |
| `-ns` | Newsstand ID (default `101`, the US newsstand) |
| `-fingerprint` | Device fingerprint presented to the Zinio API |
| `-issue` | Only process this issue ID (default `0` = all) |
| `-keeppages` | Keep per-page PDFs after merging (debugging) |

Magazines are saved to the `issue/` directory, one PDF per issue. Already-downloaded
issues are skipped, so the tool can be re-run to fetch only new purchases.

## How it works

1. Logs into Zinio and pulls the list of purchased issues (paginated).
2. For each issue, fetches the page list from `/api/reader/content` — every page is an
   individually AES-encrypted PDF, and the response carries the decryption keys
   (`hash` / `legacy_hash`).
3. Downloads each page (with retries) and verifies PDFium can open it with one of the keys.
4. PDFium imports the first page of every page file into a single document, strips the
   page-number annotations, and saves the final magazine.

No Chrome, Playwright or other external renderer is needed (unlike the original
SVG-based versions of this tool).

## Building

Requires Go 1.26+.

```
go build -o ZiniGo/ZiniGo ./ZiniGo
```

Cross-compilation scripts for Windows/Linux live in `buildscripts/`, and some
prebuilt binaries from earlier versions in `built/`.

## E-ink readers (reMarkable, etc.)

Older Zinio issues embed **Type 1 fonts**, which some e-ink readers cannot render
(they substitute fonts, mangling drop caps and text). `scripts/convert_type1_fonts.py`
converts every embedded Type 1 font to CFF without touching layout, encodings or metrics:

```
python3 scripts/convert_type1_fonts.py "issue/Magazine.pdf" "issue/remarkable/Magazine.pdf"
```

Requires [FontForge](https://fontforge.org) on the PATH (`brew install fontforge`) and the
Python packages `pikepdf` and `fonttools`. Note: Ghostscript's `pdfwrite` is **not** a
substitute — it loses glyphs with these fonts' custom `/Differences` encodings.

## License

See [LICENSE](LICENSE). Use only with magazines you have legally purchased.
