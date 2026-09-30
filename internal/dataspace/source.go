package dataspace

import (
	"errors"
	"maps"
	"slices"
)

// The source is specifically the controller's DCAT profile, not arbitrary
// JSON-LD. Exact context and types prevent renamed terms from changing meaning.
type sourceCatalog struct {
	Context  map[string]string `json:"@context"`
	ID       string            `json:"@id"`
	Type     string            `json:"@type"`
	Datasets []sourceDataset   `json:"dcat:dataset"`
	Services []sourceService   `json:"dcat:service"`
}
type reference struct {
	ID string `json:"@id"`
}
type sourceDataset struct {
	ID          string   `json:"@id"`
	Type        []string `json:"@type"`
	Identifier  string   `json:"dcterms:identifier"`
	Title       string   `json:"dcterms:title"`
	Description string   `json:"dcterms:description"`
	Version     string   `json:"dcat:version"`
	Publisher   struct {
		Type string     `json:"@type"`
		Name string     `json:"foaf:name"`
		Page *reference `json:"foaf:page,omitempty"`
	} `json:"dcterms:publisher"`
	Landing       *reference           `json:"dcat:landingPage,omitempty"`
	Distributions []sourceDistribution `json:"dcat:distribution"`
}
type sourceDistribution struct {
	ID            string    `json:"@id"`
	Type          string    `json:"@type"`
	Title         string    `json:"dcterms:title"`
	AccessURL     reference `json:"dcat:accessURL"`
	AccessService reference `json:"dcat:accessService"`
	Format        string    `json:"dcterms:format,omitempty"`
}
type sourceService struct {
	ID       string    `json:"@id"`
	Type     []string  `json:"@type"`
	Title    string    `json:"dcterms:title"`
	Endpoint reference `json:"dcat:endpointURL"`
	Contract reference `json:"dcat:endpointDescription"`
	Dataset  reference `json:"dcat:servesDataset"`
}

func (s sourceCatalog) index() (map[string]sourceDataset, map[string]bool, error) {
	fail := func() (map[string]sourceDataset, map[string]bool, error) {
		return nil, nil, errors.New("source is not a valid bounded controller DCAT catalog")
	}
	if !maps.Equal(
		s.Context,
		map[string]string{
			"dcat":    "http://www.w3.org/ns/dcat#",
			"dcterms": "http://purl.org/dc/terms/",
			"foaf":    "http://xmlns.com/foaf/0.1/",
		},
	) ||
		s.Type != "dcat:Catalog" ||
		s.Datasets == nil ||
		s.Services == nil ||
		len(s.Datasets) > maxDatasets ||
		len(s.Services) > maxDistributions {
		return fail()
	}
	ids := map[string]bool{}
	if err := claim(ids, s.ID); err != nil {
		return fail()
	}
	index := map[string]sourceDataset{}
	outputs := 0
	services := map[string]sourceService{}
	for _, v := range s.Services {
		if claim(ids, v.ID) != nil || !types(v.Type, "dcat:DataService") ||
			!publicURL(v.Endpoint.ID, false) ||
			!publicURL(v.Contract.ID, false) {
			return fail()
		}
		services[v.ID] = v
	}
	for _, d := range s.Datasets {
		if claim(ids, d.ID) != nil || !types(d.Type, "dcat:Dataset") || d.Identifier != d.ID ||
			d.Title == "" ||
			d.Description == "" ||
			d.Version == "" ||
			d.Publisher.Type != "foaf:Agent" ||
			d.Publisher.Name == "" {
			return fail()
		}
		for _, ref := range []*reference{d.Landing, d.Publisher.Page} {
			if ref != nil && !publicURL(ref.ID, false) {
				return fail()
			}
		}
		if len(d.Distributions) == 0 {
			return fail()
		}
		outputs += len(d.Distributions)
		if outputs > maxDistributions {
			return fail()
		}
		for _, v := range d.Distributions {
			service, ok := services[v.AccessService.ID]
			if claim(ids, v.ID) != nil || v.Type != "dcat:Distribution" || v.Title == "" ||
				!publicURL(v.AccessURL.ID, false) ||
				!ok ||
				service.Dataset.ID != d.ID ||
				service.Endpoint.ID != v.AccessURL.ID {
				return fail()
			}
		}
		index[d.ID] = d
	}
	for _, v := range s.Services {
		if _, ok := index[v.Dataset.ID]; !ok {
			return fail()
		}
	}
	return index, ids, nil
}

func types(got []string, kind string) bool {
	return len(got) == 2 && slices.Contains(got, kind) && slices.Contains(got, "dcat:Resource")
}
