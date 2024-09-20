package main

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// grantEntryDC is the Dublin-Code-counterpart to grantEntry.  For typing
// reasons, I have to distinguish between both.
type grantEntryDC struct {
	XMLName    xml.Name   `xml:"record"`
	Identifier string     `xml:"header>identifier"`
	Datestamp  string     `xml:"header>datestamp"`
	Metadata   metadataDC `xml:"metadata"`
}

// metadataDC is the Dublin-Code-counterpart to “metadata” in marcxml.go.
type metadataDC struct {
	Record recordDC `xml:"oai_dc:dc"`
}

// recordDC holds one Dublin Code dataset for XML output.
type recordDC struct {
	XMLnsOAIDC     string   `xml:"xmlns:oai_dc,attr"`
	XMLnsDC        string   `xml:"xmlns:dc,attr"`
	XMLnsXSI       string   `xml:"xmlns:xsi,attr"`
	SchemaLocation string   `xml:"xsi:schemaLocation,attr"`
	Title          string   `xml:"dc:title"`
	Creator        []string `xml:"dc:creator"`
	Publisher      string   `xml:"dc:publisher"`
	Identifier     string   `xml:"dc:identifier"`
	Description    string   `xml:"dc:description,omitempty"`
	Relation       string   `xml:"dc:relation,omitempty"`
	Date           string   `xml:"dc:date,omitempty"`
	Type           string   `xml:"dc:type"`
}

// buildGrantDC generates a data structure ready to be marshalled into XML that
// contains the given grant in Dublin Core format.  The top-level element is
// PMH’s <record>.
func buildGrantDC(decodeFunc func(any) error) (any, error) {
	var (
		grantDoc grantDocument
		entry    grantEntryDC
	)
	if err := decodeFunc(&grantDoc); err != nil {
		return nil, err
	}
	entry.Identifier = grantDoc.Identifier
	entry.Datestamp = grantDoc.LastModification.In(time.UTC).Format(time.RFC3339)
	entry.Metadata.Record.XMLnsOAIDC = "http://www.openarchives.org/OAI/2.0/oai_dc/"
	entry.Metadata.Record.XMLnsDC = "http://purl.org/dc/elements/1.1/"
	entry.Metadata.Record.XMLnsXSI = "http://www.w3.org/2001/XMLSchema-instance"
	entry.Metadata.Record.SchemaLocation = "http://www.openarchives.org/OAI/2.0/oai_dc/ " +
		"http://www.openarchives.org/OAI/2.0/oai_dc.xsd"
	entry.Metadata.Record.Title = grantDoc.Title
	var names []string
	names = append(names, grantDoc.Applicants...)
	if len(names) == 0 {
		names = append(names, grantDoc.Speakers...)
		if len(names) == 0 {
			names = append(names, grantDoc.Institutions...)
		}
	}
	entry.Metadata.Record.Creator = names
	entry.Metadata.Record.Publisher = "Deutsche Forschungsgemeinschaft, Bonn"
	{
		number := strings.TrimPrefix(grantDoc.Identifier, "G:(GEPRIS)")
		if number == grantDoc.Identifier {
			panic(fmt.Errorf("Not a valid GEPRIS identifier: %s", grantDoc.Parent))
		}
		entry.Metadata.Record.Identifier = fmt.Sprintf("http://gepris.dfg.de/gepris/projekt/%v", number)
	}
	if grantDoc.Parent != "" {
		number := strings.TrimPrefix(grantDoc.Parent, "G:(GEPRIS)")
		if number == grantDoc.Parent {
			panic(fmt.Errorf("Not a valid GEPRIS identifier: %s", grantDoc.Parent))
		}
		entry.Metadata.Record.Relation = fmt.Sprintf("http://gepris.dfg.de/gepris/projekt/%v", number)
	}
	entry.Metadata.Record.Description = grantDoc.Description
	entry.Metadata.Record.Date = buildTimespan(grantDoc, "/")
	entry.Metadata.Record.Type = "grant"
	return entry, nil
}
