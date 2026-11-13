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
// Metadata values are not changed.
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
	changed, err := needsRepair(data)
	if err != nil || !changed {
		return data, changed, err
	}
	fixed, err := finalizeArchive(data, false, "")
	if err != nil {
		return nil, false, err
	}
	return fixed, true, nil
}

// needsRepair reports whether any package document in the archive needs the
// Dublin Core prefix or meta-collision fixes.
func needsRepair(data []byte) (bool, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false, err
	}
	found := false
	for _, f := range zr.File {
		if !isOPF(f.Name) {
			continue
		}
		found = true
		content, err := readZipFile(f)
		if err != nil {
			return false, err
		}
		repaired := prefixDublinCore(content)
		repaired = repairMetaCollisions(repaired)
		if !bytes.Equal(repaired, content) {
			return true, nil
		}
	}
	if !found {
		return false, errors.New("no package document (.opf) found")
	}
	return false, nil
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
