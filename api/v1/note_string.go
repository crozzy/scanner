// Code generated by "stringer -type=Note"; DO NOT EDIT.

package v1

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[OSCVEsUnavailable-0]
	_ = x[OSCVEsStale-1]
	_ = x[LanguageCVEsUnavailable-2]
	_ = x[CertifiedRHELScanUnavailable-3]
	_ = x[SentinelNote-4]
}

const _Note_name = "OSCVEsUnavailableOSCVEsStaleLanguageCVEsUnavailableCertifiedRHELScanUnavailableSentinelNote"

var _Note_index = [...]uint8{0, 17, 28, 51, 79, 91}

func (i Note) String() string {
	if i < 0 || i >= Note(len(_Note_index)-1) {
		return "Note(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Note_name[_Note_index[i]:_Note_index[i+1]]
}
