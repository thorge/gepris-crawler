#!/bin/python

from oaipmh.client import Client
from oaipmh.metadata import MetadataRegistry, oai_dc_reader


registry = MetadataRegistry()
registry.registerReader("oai_dc", oai_dc_reader)
client = Client("https://gepris.its.kfa-juelich.de", registry)

identifiers = set()
for record in client.listRecords(metadataPrefix="oai_dc"):
    fields = record[1].getMap()
    identifier = fields["identifier"][0]
    identifiers.add(identifier)
    print(identifier, fields["relation"], fields["creator"])
print(len(identifiers))
