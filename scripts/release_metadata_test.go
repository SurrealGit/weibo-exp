package scripts

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"io"
	"os"
	"strings"
	"testing"
)

// test-release.sh supplies the actual archives. Read their headers directly:
// macOS extraction may silently restore attributes that GNU tar warns about.
func TestReleaseMetadata(t *testing.T) {
	archives := flag.Args()
	if len(archives) == 0 {
		t.Skip("run scripts/test-release.sh to check built release archives")
	}
	for _, name := range archives {
		t.Run(name, func(t *testing.T) {
			if strings.HasSuffix(name, ".zip") {
				archive, err := zip.OpenReader(name)
				if err != nil {
					t.Fatal(err)
				}
				defer archive.Close()
				for _, entry := range archive.File {
					checkMetadataName(t, entry.Name)
					if len(entry.Extra) != 0 {
						t.Errorf("%s: unexpected ZIP extra fields", entry.Name)
					}
				}
				return
			}
			file, err := os.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			gz, err := gzip.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			defer gz.Close()
			reader := tar.NewReader(gz)
			for {
				header, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				checkMetadataName(t, header.Name)
				if header.Uid != 0 || header.Gid != 0 || header.Uname != "root" || header.Gname != "root" {
					t.Errorf("%s: archive ownership must be normalized to root:0", header.Name)
				}
				for key := range header.PAXRecords {
					if strings.HasPrefix(key, "LIBARCHIVE.") || strings.HasPrefix(key, "SCHILY.") {
						t.Errorf("%s: unexpected PAX metadata %s", header.Name, key)
					}
				}
			}
		})
	}
}

func checkMetadataName(t *testing.T, name string) {
	t.Helper()
	for _, part := range strings.Split(name, "/") {
		if part == "__MACOSX" || strings.HasPrefix(part, "._") {
			t.Errorf("unexpected AppleDouble entry: %s", name)
		}
	}
}
