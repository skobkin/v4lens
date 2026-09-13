//go:build probe && !libyuv

package probe

import (
	"fmt"
	"image"
)

// libyuvAvailable reports that the optional CGO libyuv adapter is compiled out.
const libyuvAvailable = false

// decodeLibyuv is unavailable without the libyuv build tag.
func decodeLibyuv(_ []byte, _ Layout) (image.Image, error) {
	return nil, fmt.Errorf("libyuv: built without the libyuv tag")
}
