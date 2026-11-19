package main

import (
	"bytes"
	"fmt"

	"golang.org/x/net/html"
)

// Presentation only: providers, navigation proofs and authorization stay with IAM.
func injectLoginPreference(raw []byte, preference string) ([]byte, error) {
	if preference == "" {
		preference = "company"
	}
	if preference != "company" && preference != "team" {
		return nil, fmt.Errorf("invalid login preference")
	}
	document, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var head, meta *html.Node
	count := 0
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "head" {
			head = node
		}
		if node.Type == html.ElementNode && node.Data == "meta" {
			for _, attr := range node.Attr {
				if attr.Key == "name" && attr.Val == "auriga-login-preference" {
					count++
					meta = node
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if head == nil || count > 1 {
		return nil, fmt.Errorf("ambiguous login preference metadata")
	}
	if meta == nil {
		meta = &html.Node{Type: html.ElementNode, Data: "meta"}
		head.AppendChild(meta)
	}
	meta.Attr = []html.Attribute{{Key: "name", Val: "auriga-login-preference"}, {Key: "content", Val: preference}}
	var output bytes.Buffer
	if err := html.Render(&output, document); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
