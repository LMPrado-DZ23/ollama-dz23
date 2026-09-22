package agent

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuilderProfessionalExports(t *testing.T) {
	root := t.TempDir()
	builder, err := NewBuilderService(filepath.Join(root, "builders"))
	if err != nil {
		t.Fatal(err)
	}
	project, err := builder.Create(context.Background(), BuilderSpec{Name: "Export test", Kind: BuilderSlides})
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"pdf", "docx", "pptx"} {
		_, output, err := builder.ExportProfessional(context.Background(), project.ID, format)
		if err != nil {
			t.Fatalf("format %s: %v", format, err)
		}
		info, err := os.Stat(output)
		if err != nil || info.Size() < 100 {
			t.Fatalf("format %s output invalid: %s size=%v err=%v", format, output, info.Size(), err)
		}
		if format != "pdf" {
			archive, err := zip.OpenReader(output)
			if err != nil {
				t.Fatalf("format %s is not a readable OOXML zip: %v", format, err)
			}
			if len(archive.File) == 0 {
				t.Fatalf("format %s has no OOXML parts", format)
			}
			_ = archive.Close()
		}
	}
}
