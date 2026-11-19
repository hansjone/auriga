package main

import (
	"bytes"
	"fmt"

	"golang.org/x/net/html"
)

type sharedPortalPlacement struct {
	parentDomain string
}

// Values come only from the validated installation and enabled server adapter.
// Query strings, browser headers, and pre-existing HTML never choose them.
func injectSharedPlacementMetadata(raw []byte, parent string) ([]byte, error) {
	document, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	values := map[string]string{
		"auriga-shared-placement-enabled": "true",
		"auriga-instance-parent-domain":   parent,
	}
	var head *html.Node
	found := map[string]*html.Node{}
	ambiguous := false
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "head" {
			head = node
		}
		if node.Type == html.ElementNode && node.Data == "meta" {
			for _, attribute := range node.Attr {
				if attribute.Key == "name" {
					if _, controlled := values[attribute.Val]; controlled {
						if found[attribute.Val] != nil {
							ambiguous = true
						}
						found[attribute.Val] = node
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if head == nil || ambiguous {
		return nil, fmt.Errorf("ambiguous shared placement metadata")
	}
	// Stable order also makes the generated static shell straightforward to inspect.
	for _, name := range []string{"auriga-shared-placement-enabled", "auriga-instance-parent-domain"} {
		tag := found[name]
		if tag == nil {
			tag = &html.Node{Type: html.ElementNode, Data: "meta"}
			head.AppendChild(tag)
		}
		tag.Attr = []html.Attribute{{Key: "name", Val: name}, {Key: "content", Val: values[name]}}
	}
	var output bytes.Buffer
	if err := html.Render(&output, document); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
