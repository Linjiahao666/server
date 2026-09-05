package files

import (
	"errors"
	"strconv"
	"strings"
)

// ByteRange describes an inclusive byte range within a file.
type ByteRange struct {
	Start int64
	End   int64
}

// ErrRangeNotSatisfiable indicates the requested range is invalid.
var ErrRangeNotSatisfiable = errors.New("range not satisfiable")

// ParseRangeHeader parses an HTTP Range header for a file of the given size.
func ParseRangeHeader(header string, size int64) (ByteRange, bool, error) {
	if header == "" {
		return ByteRange{}, false, nil
	}

	if !strings.HasPrefix(header, "bytes=") {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}

	spec := strings.TrimSpace(strings.TrimPrefix(header, "bytes="))
	if spec == "" {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}

	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}

	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return ByteRange{}, false, ErrRangeNotSatisfiable
		}
		if suffix > size {
			suffix = size
		}
		return ByteRange{Start: size - suffix, End: size - 1}, true, nil
	}

	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}

	end := size - 1
	if parts[1] != "" {
		parsedEnd, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || parsedEnd < start {
			return ByteRange{}, false, ErrRangeNotSatisfiable
		}
		end = parsedEnd
	}
	if end >= size {
		end = size - 1
	}

	return ByteRange{Start: start, End: end}, true, nil
}
