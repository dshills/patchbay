package decksetup

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// plist preserves opaque preference values (including dates and binary data).
// Converting preferences through JSON would lose their property-list types.
type plist struct {
	Kind     string
	Text     string
	Children []*plist
}

func parsePlist(data []byte) (*plist, error) {
	if len(data) > 16<<20 {
		return nil, errors.New("the Stream Deck preferences exceed the setup limit")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	var read func(xml.StartElement, int) (*plist, error)
	read = func(start xml.StartElement, depth int) (*plist, error) {
		if depth > 32 {
			return nil, errors.New("preferences are too deeply nested")
		}
		p := &plist{Kind: start.Name.Local}
		for {
			t, err := d.Token()
			if err != nil {
				return nil, err
			}
			switch t := t.(type) {
			case xml.StartElement:
				child, err := read(t, depth+1)
				if err != nil {
					return nil, err
				}
				p.Children = append(p.Children, child)
			case xml.CharData:
				p.Text += string(t)
			case xml.EndElement:
				return p, nil
			}
		}
	}
	for {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := t.(xml.StartElement); ok {
			p, err := read(start, 0)
			if err != nil {
				return nil, err
			}
			if p.Kind != "plist" || len(p.Children) != 1 || p.Children[0].Kind != "dict" {
				return nil, errors.New("unsupported Stream Deck preferences")
			}
			if err := validPlist(p.Children[0]); err != nil {
				return nil, err
			}
			for {
				token, e := d.Token()
				err = e
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
				if _, ok := token.(xml.StartElement); ok {
					return nil, errors.New("extra property-list document")
				}
			}
			return p.Children[0], nil
		}
	}
}
func validPlist(p *plist) error {
	bad := errors.New("invalid property-list value")
	switch p.Kind {
	case "dict":
		if len(p.Children)%2 != 0 {
			return bad
		}
		seen := map[string]bool{}
		for i := 0; i < len(p.Children); i += 2 {
			key := p.Children[i]
			if key.Kind != "key" || len(key.Children) != 0 || seen[key.Text] {
				return bad
			}
			seen[key.Text] = true
			if err := validPlist(p.Children[i+1]); err != nil {
				return err
			}
		}
	case "array":
		for _, c := range p.Children {
			if err := validPlist(c); err != nil {
				return err
			}
		}
	case "string", "data", "date", "integer", "real":
		if len(p.Children) != 0 {
			return bad
		}
	case "true", "false":
		if len(p.Children) != 0 || strings.TrimSpace(p.Text) != "" {
			return bad
		}
	default:
		return bad
	}
	return nil
}
func (p *plist) get(key string) *plist {
	if p == nil || p.Kind != "dict" {
		return nil
	}
	for i := 0; i+1 < len(p.Children); i += 2 {
		if p.Children[i].Kind == "key" && p.Children[i].Text == key {
			return p.Children[i+1]
		}
	}
	return nil
}
func (p *plist) set(key string, value *plist) {
	for i := 0; i+1 < len(p.Children); i += 2 {
		if p.Children[i].Text == key {
			p.Children[i+1] = value
			return
		}
	}
	p.Children = append(p.Children, &plist{Kind: "key", Text: key}, value)
}
func (p *plist) string() string {
	if p == nil || p.Kind != "string" {
		return ""
	}
	return p.Text
}
func (p *plist) bytes() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(xml.Header + "<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\">")
	e := xml.NewEncoder(&b)
	var write func(*plist) error
	write = func(p *plist) error {
		s := xml.StartElement{Name: xml.Name{Local: p.Kind}}
		if err := e.EncodeToken(s); err != nil {
			return err
		}
		if len(p.Children) == 0 {
			if err := e.EncodeToken(xml.CharData(p.Text)); err != nil {
				return err
			}
		} else {
			for _, c := range p.Children {
				if err := write(c); err != nil {
					return err
				}
			}
		}
		return e.EncodeToken(s.End())
	}
	if err := write(p); err != nil {
		return nil, err
	}
	if err := e.Flush(); err != nil {
		return nil, err
	}
	b.WriteString("</plist>\n")
	return b.Bytes(), nil
}
