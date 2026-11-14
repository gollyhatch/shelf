package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"time"
)

// rewriteArchive returns a new archive with the named entries replaced by the
// given content. Every other entry is copied in compressed form, so chapters
// and images are not decompressed or re-compressed. The OCF mimetype entry is
// always written first and stored.
func rewriteArchive(zr *zip.Reader, replace map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(Mimetype)); err != nil {
		return nil, err
	}

	for _, f := range zr.File {
		if f.Name == "mimetype" {
			continue
		}
		if content, ok := replace[f.Name]; ok {
			hdr := f.FileHeader
			// Keep the entry's original MS-DOS timestamp instead of the
			// interpreted time, so the rewritten entry looks untouched.
			hdr.Modified = time.Time{}
			w, err := zw.CreateHeader(&hdr)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(content); err != nil {
				return nil, err
			}
			continue
		}
		if err := zw.Copy(f); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
