package agent

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

func (b *BuilderService) ExportProfessional(ctx context.Context, id, format string) (BuilderProject, string, error) {
	if err := ctx.Err(); err != nil {
		return BuilderProject{}, "", err
	}
	project, err := b.Get(id)
	if err != nil {
		return BuilderProject{}, "", err
	}
	format = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), "."))
	if format != "pdf" && format != "docx" && format != "pptx" {
		return BuilderProject{}, "", errors.New("professional export format must be pdf, docx, or pptx")
	}
	entry, err := os.ReadFile(filepath.Join(project.Root, filepath.FromSlash(project.Entry)))
	if err != nil {
		return BuilderProject{}, "", err
	}
	text := strings.TrimSpace(string(entry))
	path := filepath.Join(b.root, id+"."+format)
	switch format {
	case "pdf":
		err = writeMinimalPDF(path, project.Name, text)
	case "docx":
		err = writeMinimalDOCX(path, project.Name, text)
	case "pptx":
		err = writeMinimalPPTX(path, project.Name, text)
	}
	return project, path, err
}

func writeMinimalPDF(path, title, body string) error {
	body = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(body, "\\", "\\\\"), "(", "\\("), ")", "\\)")
	body = strings.ReplaceAll(body, "\n", " ")
	if len(body) > 4000 {
		body = body[:4000]
	}
	content := fmt.Sprintf("BT /F1 12 Tf 50 760 Td (%s) Tj ET", body)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var document strings.Builder
	document.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := document.Len()
	fmt.Fprintf(&document, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R /Info << /Title (%s) >> >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, strings.ReplaceAll(title, ")", ""), xref)
	return os.WriteFile(path, []byte(document.String()), 0o600)
}

func writeMinimalDOCX(path, title, body string) error {
	text := html.EscapeString(strings.TrimSpace(body))
	return writeZip(path, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + html.EscapeString(title) + `</w:t></w:r></w:p><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p><w:sectPr/></w:body></w:document>`,
	})
}

func writeMinimalPPTX(path, title, body string) error {
	return writeZip(path, map[string]string{
		"[Content_Types].xml":             `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/></Types>`,
		"_rels/.rels":                     `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>`,
		"ppt/presentation.xml":            `<?xml version="1.0" encoding="UTF-8"?><p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":           `<?xml version="1.0" encoding="UTF-8"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr/><p:grpSpPr/><p:sp><p:nvSpPr/><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US"/><a:t>` + html.EscapeString(title+" — "+body) + `</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
	})
}

func writeZip(path string, files map[string]string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(file)
	for name, content := range files {
		writer, createErr := archive.Create(name)
		if createErr != nil {
			_ = file.Close()
			return createErr
		}
		if _, writeErr := writer.Write([]byte(content)); writeErr != nil {
			_ = file.Close()
			return writeErr
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
