// Package online fetches book metadata from an online catalogue.
package online

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gollyhatch/shelf/internal/epub"
)

// DefaultBaseURL is the Google Books volumes endpoint.
const DefaultBaseURL = "https://www.googleapis.com/books/v1/volumes"

var (
	isbnPattern = regexp.MustCompile(`^(?:\d{9}[\dX]|\d{13})$`)
	htmlTag     = regexp.MustCompile(`<[^>]*>`)
)

// Client fetches metadata from a Google Books-compatible endpoint. The fields
// are exported so tests can point it at a local server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	APIKey  string
}

var defaultClient = &Client{
	BaseURL: DefaultBaseURL,
	HTTP:    &http.Client{Timeout: 15 * time.Second},
}

// Lookup searches for metadata using the client's default configuration.
func Lookup(meta epub.Metadata) (epub.Metadata, error) {
	return defaultClient.Lookup(meta)
}

// Lookup searches for a book using its ISBN when available, otherwise its
// title and author.
func (c *Client) Lookup(meta epub.Metadata) (epub.Metadata, error) {
	query, err := searchQuery(meta)
	if err != nil {
		return epub.Metadata{}, err
	}

	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil {
		return epub.Metadata{}, fmt.Errorf("online lookup: %w", err)
	}
	params := u.Query()
	params.Set("q", query)
	params.Set("maxResults", "1")
	params.Set("printType", "books")
	if c.APIKey != "" {
		params.Set("key", c.APIKey)
	}
	u.RawQuery = params.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return epub.Metadata{}, fmt.Errorf("online lookup: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "shelf")

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return epub.Metadata{}, fmt.Errorf("online lookup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return epub.Metadata{}, fmt.Errorf("online lookup: %s", resp.Status)
	}

	var payload struct {
		Items []struct {
			VolumeInfo volumeInfo `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return epub.Metadata{}, fmt.Errorf("online lookup: %w", err)
	}
	if len(payload.Items) == 0 {
		return epub.Metadata{}, errors.New("no online match found")
	}
	return metadataFrom(payload.Items[0].VolumeInfo), nil
}

type volumeInfo struct {
	Title               string   `json:"title"`
	Subtitle            string   `json:"subtitle"`
	Authors             []string `json:"authors"`
	Publisher           string   `json:"publisher"`
	PublishedDate       string   `json:"publishedDate"`
	Description         string   `json:"description"`
	Categories          []string `json:"categories"`
	Language            string   `json:"language"`
	IndustryIdentifiers []struct {
		Type       string `json:"type"`
		Identifier string `json:"identifier"`
	} `json:"industryIdentifiers"`
}

func metadataFrom(v volumeInfo) epub.Metadata {
	var creators []epub.Creator
	for _, author := range v.Authors {
		if name := cleanText(author); name != "" {
			creators = append(creators, epub.Creator{Name: name})
		}
	}
	var subjects []string
	for _, category := range v.Categories {
		if category = cleanText(category); category != "" {
			subjects = append(subjects, category)
		}
	}
	identifier := ""
	for _, preferred := range []string{"ISBN_13", "ISBN_10"} {
		for _, id := range v.IndustryIdentifiers {
			if id.Type == preferred && id.Identifier != "" {
				identifier = id.Identifier
				break
			}
		}
		if identifier != "" {
			break
		}
	}
	return epub.Metadata{
		Title:       cleanText(v.Title),
		Subtitle:    cleanText(v.Subtitle),
		Creators:    creators,
		Subjects:    subjects,
		Description: cleanText(v.Description),
		Publisher:   cleanText(v.Publisher),
		Date:        cleanText(v.PublishedDate),
		Language:    cleanText(v.Language),
		Identifier:  cleanText(identifier),
	}
}

func searchQuery(meta epub.Metadata) (string, error) {
	if isbn := isbnFromIdentifier(meta.Identifier); isbn != "" {
		return "isbn:" + isbn, nil
	}
	title := strings.TrimSpace(meta.Title)
	author := strings.TrimSpace(meta.AuthorString())
	switch {
	case title != "" && author != "":
		return title + " " + author, nil
	case title != "":
		return title, nil
	case author != "":
		return author, nil
	}
	return "", errors.New("not enough metadata to search online (need an ISBN, title or author)")
}

func isbnFromIdentifier(identifier string) string {
	s := strings.ToLower(strings.TrimSpace(identifier))
	s = strings.TrimPrefix(s, "urn:isbn:")
	s = strings.TrimPrefix(s, "isbn:")
	s = strings.TrimPrefix(s, "isbn ")
	s = strings.TrimPrefix(s, "isbn-13:")
	s = strings.TrimPrefix(s, "isbn-10:")
	s = strings.NewReplacer("-", "", " ", "", "_", "").Replace(s)
	if isbnPattern.MatchString(strings.ToUpper(s)) {
		return strings.ToUpper(s)
	}
	return ""
}

// cleanText removes HTML markup and collapses whitespace.
func cleanText(s string) string {
	s = htmlTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}
