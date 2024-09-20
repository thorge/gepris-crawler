#!/bin/python

"""This test script harvests a local PMH server and checks whether it returns a
plausible list of entries using resumption tokens.
"""

import urllib.request, gzip
from xml.etree import ElementTree


def open_url(url):
    request = urllib.request.Request(url)
    request.add_header("Accept-encoding", "gzip")
    stream = urllib.request.urlopen(request)
    return gzip.GzipFile(fileobj=stream)


ids = []

def collect_ids(tree):
    result = []
    for element in tree.findall(".//{http://www.openarchives.org/OAI/2.0/}identifier"):
        result.append(element.text)
    return result

def get_tree(resumption_token):
    if resumption_token:
        tree = ElementTree.parse(open_url("https://gepris.its.kfa-juelich.de?verb=ListRecords&resumptionToken=" +
                                          resumption_token))
    else:
        tree = ElementTree.parse(open_url("https://gepris.its.kfa-juelich.de?verb=ListRecords&metadataPrefix=marcxml"))
    ids.extend(collect_ids(tree))
    return tree.find(".//{http://www.openarchives.org/OAI/2.0/}resumptionToken").text

resumption_token = None
while True:
    resumption_token = get_tree(resumption_token)
    print(resumption_token)
    if not resumption_token:
        break

assert len(ids) == len(set(ids))
print(len(ids), len(set(ids)), ids[:10])
