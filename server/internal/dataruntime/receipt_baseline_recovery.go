package dataruntime

type receiptBaselineWALScan struct {
	marker           receiptBaselineMarker
	markerSequence   uint64
	firstGeneration  [16]byte
	activeGeneration [16]byte
	hasMarker        bool
}
