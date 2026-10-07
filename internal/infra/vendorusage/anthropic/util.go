package anthropic

import "io"

// readAll consumes up to max bytes from r. Used to extract a bounded
// error-body snippet for non-2xx HTTP responses so error messages stay
// useful without blowing memory on a huge upstream payload.
func readAll(r io.Reader, max int) (string, error) {
	buf := make([]byte, max)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return string(buf[:n]), err
	}
	return string(buf[:n]), nil
}
