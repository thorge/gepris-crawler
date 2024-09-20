package main

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// grantEntry represents the top-level XML element in the PMH response.  In the
// XML sample at
// <http://www.openarchives.org/OAI/openarchivesprotocol.html#ListRecords>,
// this element is called “oai_dc:dc”.
type grantEntry struct {
	XMLName    xml.Name `xml:"record"`
	Identifier string   `xml:"header>identifier"`
	Datestamp  string   `xml:"header>datestamp"`
	Metadata   metadata `xml:"metadata"`
}

// metadata is a PMH element containing the top-level non-PMH element.
type metadata struct {
	Record record `xml:"record"`
}

// record is the MARCXML element of the same name.
type record struct {
	XMLns          string `xml:"xmlns,attr"`
	XMLnsXSI       string `xml:"xmlns:xsi,attr"`
	SchemaLocation string `xml:"xsi:schemaLocation,attr"`
	Datafields     []datafield
}

// datafield is the MARCXML element of the same name.
type datafield struct {
	XMLName   xml.Name `xml:"datafield"`
	Tag       string   `xml:"tag,attr"`
	Ind1      string   `xml:"ind1,attr"`
	Ind2      string   `xml:"ind2,attr"`
	Subfields []subfield
}

// subfield is the MARCXML element of the same name.
type subfield struct {
	XMLName xml.Name `xml:"subfield"`
	Code    string   `xml:"code,attr"`
	Content string   `xml:",chardata"`
}

// buildGrant creates one MARCXML record for the given grant.  Actually, it
// returns a PMH <record> element, which contains slightly more than only the
// MARCXML.
func buildGrant(decodeFunc func(any) error) (any, error) {
	var (
		grantDoc grantDocument
		entry    grantEntry
	)
	if err := decodeFunc(&grantDoc); err != nil {
		return nil, err
	}
	geprisID := grantDoc.Identifier
	entry.Identifier = geprisID
	entry.Datestamp = grantDoc.LastModification.In(time.UTC).Format(time.RFC3339)
	entry.Metadata.Record.XMLns = "http://www.loc.gov/MARC21/slim"
	entry.Metadata.Record.XMLnsXSI = "http://www.w3.org/2001/XMLSchema-instance"
	entry.Metadata.Record.SchemaLocation = "http://www.loc.gov/MARC21/slim " +
		"https://www.loc.gov/standards/marcxml/schema/MARC21slim.xsd"
	timespan := buildTimespan(grantDoc, " - ")
	datafields := make([]datafield, 0, 6)
	number := strings.TrimPrefix(grantDoc.Identifier, "G:(GEPRIS)")
	if number == grantDoc.Identifier {
		panic(fmt.Errorf("Not a valid GEPRIS identifier: %s", grantDoc.Identifier))
	}
	datafields = append(datafields,
		datafield{
			XMLName:   xml.Name{},
			Tag:       "024",
			Ind1:      "7",
			Ind2:      " ",
			Subfields: []subfield{{Code: "a", Content: geprisID}, {Code: "d", Content: number}},
		})
	datafields = append(datafields,
		datafield{Tag: "040", Ind1: " ", Ind2: " ",
			Subfields: []subfield{
				{Code: "a", Content: "GEPRIS"},
				{Code: "c", Content: "http://gepris.its.kfa-juelich.de"},
			}})
	datafields = append(datafields,
		datafield{Tag: "150", Ind1: " ", Ind2: " ",
			Subfields: []subfield{
				{Code: "a", Content: grantDoc.Title},
				{Code: "y", Content: timespan},
			}})
	var names []string
	names = append(names, grantDoc.Applicants...)
	if len(names) == 0 {
		names = append(names, grantDoc.Speakers...)
		if len(names) == 0 {
			names = append(names, grantDoc.Institutions...)
		}
	}
	for _, applicant := range names {
		datafields = append(datafields,
			datafield{Tag: "371", Ind1: " ", Ind2: " ",
				Subfields: []subfield{{Code: "a", Content: applicant}}})
	}
	if grantDoc.ShortTitle != "" {
		datafields = append(datafields,
			datafield{Tag: "450", Ind1: " ", Ind2: " ",
				Subfields: []subfield{
					{Code: "a", Content: grantDoc.ShortTitle},
					{Code: "w", Content: "d"},
					{Code: "y", Content: timespan},
				}})
	}
	datafields = append(datafields,
		datafield{Tag: "510", Ind1: "1", Ind2: " ",
			Subfields: []subfield{
				{Code: "a", Content: "Deutsche Forschungsgemeinschaft"},
				{Code: "0", Content: "I:(DE-588b)2007744-0"},
				{Code: "b", Content: "DFG"},
			}})
	if grantDoc.Parent != "" {
		datafields = append(datafields,
			datafield{Tag: "550", Ind1: " ", Ind2: " ",
				Subfields: []subfield{
					{Code: "0", Content: grantDoc.Parent},
					{Code: "a", Content: grantDoc.ParentTitle},
					{Code: "w", Content: "t"},
				}})
	}
	datafields = append(datafields,
		datafield{Tag: "680", Ind1: " ", Ind2: " ",
			Subfields: []subfield{{Code: "a", Content: grantDoc.Description}}})
	entry.Metadata.Record.Datafields = datafields
	return entry, nil
}

// buildTimespan returns the years of funding of the given grant as an interval
// ready to be used in MARCXML output (with “ - ” as sep) or Dublin Core (with
// “/” as sep).  If no funding years are contained in the grant, an empty
// string is returned.
func buildTimespan(doc grantDocument, sep string) string {
	var sb strings.Builder
	if doc.StartYear != 0 {
		sb.WriteString(strconv.Itoa(doc.StartYear))
		if doc.EndYear != doc.StartYear {
			if doc.EndYear != 0 {
				sb.WriteString(sep)
				sb.WriteString(strconv.Itoa(doc.EndYear))
			} else {
				sb.WriteString(strings.TrimSuffix(sep, " "))
			}
		}
	} else {
		if doc.EndYear != 0 {
			sb.WriteString(strings.TrimPrefix(sep, " "))
			sb.WriteString(strconv.Itoa(doc.EndYear))
		}
	}
	return sb.String()
}
