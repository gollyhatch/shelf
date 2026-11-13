package online

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gollyhatch/shelf/internal/epub"
)

func TestLookupByISBN(t *testing.T) {
	var query, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		key = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[{"volumeInfo":{
			"title":"Dune",
			"subtitle":"A Novel",
			"authors":["Frank Herbert"],
			"publisher":"Chilton",
			"publishedDate":"1965",
			"description":"<p>A desert planet.</p>",
			"categories":["Fiction / Science Fiction"],
			"language":"en",
			"industryIdentifiers":[{"type":"ISBN_10","identifier":"0441013597"},{"type":"ISBN_13","identifier":"9780441013593"}]
		}}]}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client(), APIKey: "test-key"}
	meta, err := client.Lookup(epub.Metadata{Identifier: "urn:isbn:978-0-441-01359-3"})
	if err != nil {
		t.Fatal(err)
	}
	if query != "isbn:9780441013593" {
		t.Errorf("query = %q", query)
	}
	if key != "test-key" {
		t.Errorf("API key = %q", key)
	}
	if meta.Title != "Dune" || meta.Subtitle != "A Novel" {
		t.Errorf("title fields = %+v", meta)
	}
	if meta.AuthorString() != "Frank Herbert" {
		t.Errorf("authors = %q", meta.AuthorString())
	}
	if meta.Description != "A desert planet." {
		t.Errorf("description = %q", meta.Description)
	}
	if meta.Identifier != "9780441013593" {
		t.Errorf("identifier = %q", meta.Identifier)
	}
	if len(meta.Subjects) != 1 || meta.Subjects[0] != "Fiction / Science Fiction" {
		t.Errorf("subjects = %v", meta.Subjects)
	}
}

func TestLookupByTitleAndAuthor(t *testing.T) {
	var query, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		key = r.URL.Query().Get("key")
		w.Write([]byte(`{"items":[{"volumeInfo":{"title":"The Left Hand of Darkness","authors":["Ursula K. Le Guin"],"language":"en"}}]}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	meta, err := client.Lookup(epub.Metadata{
		Title:    "The Left Hand of Darkness",
		Creators: []epub.Creator{{Name: "Ursula K. Le Guin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if query != "The Left Hand of Darkness Ursula K. Le Guin" {
		t.Errorf("query = %q", query)
	}
	if key != "" {
		t.Errorf("key = %q, want no key parameter", key)
	}
	if meta.Title != "The Left Hand of Darkness" {
		t.Errorf("title = %q", meta.Title)
	}
}

func TestLookupNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := client.Lookup(epub.Metadata{Title: "Does Not Exist"}); err == nil {
		t.Fatal("expected no-match error")
	}
}

func TestLookupHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := client.Lookup(epub.Metadata{Title: "Dune"}); err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %v", err)
	}
}

func TestLookupNeedsMetadata(t *testing.T) {
	if _, err := (&Client{}).Lookup(epub.Metadata{}); err == nil {
		t.Fatal("expected an insufficient-metadata error")
	}
}

func TestISBNFromIdentifier(t *testing.T) {
	tests := map[string]string{
		"978-0-441-01359-3":     "9780441013593",
		"urn:isbn:0441013597":   "0441013597",
		"ISBN 0-441-01359-7":    "0441013597",
		"044101359x":            "044101359X",
		"urn:uuid:1234-5678-90": "",
		"":                      "",
	}
	for in, want := range tests {
		if got := isbnFromIdentifier(in); got != want {
			t.Errorf("isbnFromIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}
