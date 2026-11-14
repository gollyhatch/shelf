package epub

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"strings"
)

// Repair rewrites the package document of an EPUB to restore the conventional
// dc: prefixes on Dublin Core elements and to normalize colliding meta keys.
// It exists for books damaged by older shelf releases; the current writer emits
// prefixed elements and reads colliding metas, so repair is not a prerequisite
// for editing. Metadata values are not changed.
//
// It reports whether the file needed a rewrite. With dryRun set, the file is
// inspected but not written.
func Repair(name string, dryRun bool) (changed bool, err error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return false, err
	}
	fixed, changed, err := repairArchive(data)
	if err != nil {
		return false, err
	}
	if !changed || dryRun {
		return changed, nil
	}
	if err := writeFileAtomic(name, fixed); err != nil {
		return false, err
	}
	return true, nil
}

// repairArchive applies the package-document fixes to an EPUB archive and
// reports whether anything changed.
func repairArchive(data []byte) ([]byte, bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false, err
	}

	found := false
	replace := make(map[string][]byte)
	for _, f := range zr.File {
		if !isOPF(f.Name) {
			continue
		}
		found = true
		content, err := readZipFile(f)
		if err != nil {
			return nil, false, err
		}
		fixed := repairMetaCollisions(prefixDublinCore(content))
		if !bytes.Equal(fixed, content) {
			replace[f.Name] = fixed
		}
	}
	if !found {
		return nil, false, errors.New("no package document (.opf) found")
	}
	if len(replace) == 0 {
		return data, false, nil
	}
	fixed, err := rewriteArchive(zr, replace)
	if err != nil {
		return nil, false, err
	}
	return fixed, true, nil
}

// isOPF reports whether a ZIP entry is a package document, regardless of the
// suffix's case.
func isOPF(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".opf")
}

// isNCX reports whether a ZIP entry is a legacy navigation document.
func isNCX(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".ncx")
}
