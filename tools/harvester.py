#!/bin/python

"""This test script harvests a local PMH server and checks whether it returns a
plausible list of entries using resumption tokens.
"""

import urllib.request, gzip, sys
from xml.etree import ElementTree


def open_url(url):
    request = urllib.request.Request(url)
    request.add_header("Accept-encoding", "gzip")
    stream = urllib.request.urlopen(request)
    return gzip.GzipFile(fileobj=stream)


records = {}

def collect_records(resumption_token):
    url = "https://gepris.its.kfa-juelich.de?verb=ListRecords&" + \
        ("resumptionToken=" + resumption_token if resumption_token else "metadataPrefix=marcxml")
    content = open_url(url).read().decode()
    tree = ElementTree.fromstring(content)
    for record in tree.findall(".//{http://www.openarchives.org/OAI/2.0/}record"):
        id_ = record.find(".//{http://www.openarchives.org/OAI/2.0/}identifier").text
        assert id_ not in records, id_
        records[id_] = record
    return tree.find(".//{http://www.openarchives.org/OAI/2.0/}resumptionToken").text


resumption_token = None
while True:
    resumption_token = collect_records(resumption_token)
    print(resumption_token, file=sys.stderr)
    if not resumption_token:
        break

print(len(records), file=sys.stderr)

for id_ in sorted(records):
    record = records[id_]
    print(ElementTree.tostring(record, encoding="unicode"))
