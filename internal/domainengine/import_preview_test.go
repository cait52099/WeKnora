package domainengine

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestPromptPreviewDOCXIsPlainProse(t *testing.T) {
	var raw bytes.Buffer
	archive := zip.NewWriter(&raw)
	document, err := archive.Create("word/document.xml")
	must(t, err)
	_, err = document.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>提取 &amp; 校验</w:t></w:r></w:p></w:body></w:document>`))
	must(t, err)
	outside, err := archive.Create("../unrelated.xml")
	must(t, err)
	_, err = outside.Write([]byte("ignore this archive member"))
	must(t, err)
	must(t, archive.Close())
	text, err := PromptPreview("docx", raw.Bytes())
	must(t, err)
	if text != "提取 & 校验\n" {
		t.Fatalf("preview = %q", text)
	}
	if _, err = PromptPreview("txt", []byte{0xff}); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
	if err = ValidateExample([]byte(`{"sample":null}`)); err != nil {
		t.Fatal(err)
	}
	if err = ValidateExample([]byte(`{"sample":null,"sample":[]}`)); err == nil {
		t.Fatal("accepted duplicate example property")
	}
}
