package report

// embedGongwenFonts intentionally keeps the DOCX generator free of bundled
// font binaries. The OOXML styles still reference standard Chinese document
// fonts, so Word/WPS can render them when installed, while the repository does
// not redistribute fonts with unclear licensing.
func embedGongwenFonts(_ *Docx) {}
