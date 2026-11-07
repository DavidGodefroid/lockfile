# lockfile plugins

A plugin answers **one yes/no question about one file**, fixed in its signed
manifest, about format, structure, integrity or rendering, never about what
the content means or says. Plugins take no parameters: with one bit of
output, a parameter would let the agent aim that bit at the content.

Official plugins live in
[lockfile-marketplace](https://github.com/DavidGodefroid/lockfile-marketplace),
whose signer is pinned in the binary. Its source holds 13 plugins:

| Plugin | Question |
|---|---|
| `file.non-empty` | Does the file contain at least one byte? |
| `format.is-pdf` | Does the file start like a PDF? |
| `pdf.parses` | Does the file parse as a PDF without errors? |
| `pdf.truncated` | Does the PDF look truncated? |
| `pdf.encrypted` | Is the PDF encrypted? |
| `pdf.has-blank-page` | Does the PDF have at least one page that paints nothing visible? |
| `pdf.all-pages-blank` | Does every page of the PDF paint nothing visible? |
| `pdf.fonts-not-embedded` | Does the PDF use at least one font that is not embedded? |
| `pdf.has-javascript` | Does the PDF contain JavaScript? |
| `pdf.has-incremental-update` | Was the PDF modified by at least one incremental update after its first save? |
| `pdf.has-non-a4-page` | Does the PDF have at least one page whose visible size is not A4? |
| `pdf.has-rotated-page` | Does the PDF have at least one page with a `/Rotate` other than 0? |
| `pdf.claims-pdfa` | Does the PDF declare PDF/A conformance in its XMP metadata? |

```sh
lockfile plugin install pdf.parses@1.0.0        # exact versions only; signatures checked with cosign
lockfile plugin update pdf.parses@1.1.0         # shows the manifest diff; a changed question needs the name typed
lockfile plugin list | verify [NAME] | remove NAME
lockfile marketplace add NAME --index URL --identity-regexp REGEXP
```

A running console applies a newly installed plugin after `:review`
(`lox attach NAME`). An unofficial marketplace needs its name typed when
added, is flagged on every approval screen and in every agent result, and is
never auto-approved.

**Writing a plugin**: see [plugin-contract.md](plugin-contract.md).
