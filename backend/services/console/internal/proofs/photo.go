package proofs

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mseptiaan/loong.lat/backend/services/console/internal/ai"
)

// errPhoto is a proof photo that cannot be analysed, whatever is tried again.
var errPhoto = errors.New("the proof photo cannot be analysed")

// decodePhoto splits the data URL the driver app sends (data:<mime>;base64,<data>) into the photo's type and bytes.
// The type must be one the AI reads and the photo within the gateway's size limit. The errors name the problem and
// never quote the photo.
func decodePhoto(dataURL string) (mime string, data []byte, err error) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return "", nil, fmt.Errorf("%w: not a data URL", errPhoto)
	}
	head, body, ok := strings.Cut(rest, ",")
	if !ok {
		return "", nil, fmt.Errorf("%w: not a data URL", errPhoto)
	}
	declared, params, _ := strings.Cut(head, ";")
	if !slices.Contains(strings.Split(params, ";"), "base64") {
		return "", nil, fmt.Errorf("%w: not base64", errPhoto)
	}
	if mime, ok = ai.ProofImageType(declared); !ok {
		return "", nil, fmt.Errorf("%w: type %.40q", errPhoto, declared)
	}
	// The padding makes the estimate up to two bytes high: only a photo clearly over the limit is refused before the
	// bytes are allocated, and the exact size is checked after.
	if base64.StdEncoding.DecodedLen(len(body)) > ai.MaxImageBytes+2 {
		return "", nil, fmt.Errorf("%w: over %d bytes", errPhoto, ai.MaxImageBytes)
	}
	if data, err = base64.StdEncoding.DecodeString(body); err != nil {
		return "", nil, fmt.Errorf("%w: the data is not valid base64", errPhoto)
	}
	if len(data) == 0 || len(data) > ai.MaxImageBytes {
		return "", nil, fmt.Errorf("%w: %d bytes", errPhoto, len(data))
	}
	return mime, data, nil
}
