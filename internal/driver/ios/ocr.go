package ios

// ocrLine is one line of text OCR found, with a box normalized to 0..1 from
// the image's top-left corner.
type ocrLine struct {
	Text       string
	Confidence float32
	X, Y, W, H float64
}

type ocrOptions struct {
	Fast      bool     // the fast recognizer: lower accuracy, no language correction
	Languages []string // in priority order, e.g. "zh-Hans", "en-US"
	// AutoLanguage lets Vision detect the language. It can load extra
	// models on the fly, which has taken over ten seconds.
	AutoLanguage bool
}
