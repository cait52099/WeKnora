package domainengine

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// PromptPreview extracts editable prose from TXT or WordprocessingML. It
// neither expands external relationships nor executes embedded content.
func PromptPreview(kind string, raw []byte) (string, error) {
	const limit = 1 << 20
	if len(raw) > limit {
		return "", errors.New("prompt exceeds size limit")
	}
	if kind == "txt" {
		raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
		if !utf8.Valid(raw) {
			return "", errors.New("invalid UTF-8")
		}
		return string(raw), nil
	}
	if kind != "docx" {
		return "", errors.New("unsupported prompt format")
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return "", err
	}
	var document *zip.File
	for _, entry := range archive.File {
		if entry.Name == "word/document.xml" {
			if document != nil {
				return "", errors.New("duplicate Word document XML")
			}
			document = entry
		}
	}
	if document == nil || document.UncompressedSize64 > limit {
		return "", errors.New("missing or oversized Word document XML")
	}
	file, err := document.Open()
	if err != nil {
		return "", err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if len(body) > limit {
		return "", errors.New("Word document XML exceeds size limit")
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var text strings.Builder
	const ns = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch tag := token.(type) {
		case xml.StartElement:
			if tag.Name.Space == ns && tag.Name.Local == "t" {
				var value string
				if err := decoder.DecodeElement(&value, &tag); err != nil {
					return "", err
				}
				text.WriteString(value)
			}
		case xml.EndElement:
			if tag.Name.Space == ns && tag.Name.Local == "p" {
				text.WriteByte('\n')
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("Word document contains no prompt text")
	}
	return text.String(), nil
}

func ValidateExample(raw []byte) error {
	if len(raw) > maxProfileBytes {
		return errors.New("example exceeds size limit")
	}
	return checkJSON(raw)
}
