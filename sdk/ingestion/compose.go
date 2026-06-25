package ingestion

import "fmt"

// ConvertCompose parses a Docker Compose document and returns the normalized document.
func ConvertCompose(data []byte) (*Document, error) {
	doc, err := Convert(data)
	if err != nil {
		return nil, err
	}
	if doc.Raw["services"] == nil {
		return nil, fmt.Errorf("compose manifest missing services")
	}
	return doc, nil
}
