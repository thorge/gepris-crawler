Todos
=====

A good template for a DFG grant as MARCXML is
<https://juser.fz-juelich.de/record/909442/export/xm>.


Things to discuss
-----------------

- How do we deal with the mix of institutions and people in MARC field 371?  In
  particular, may also be an institution in field 371?
- Is the out-most element “collection” in the MARCXML output correct?
  http://www.openarchives.org/OAI/2.0/guidelines-marcxml.htm says otherwise.
  The Dublin Code output in the PMH spec contains an out-most element that is
  *not* part of Dublin Core but of OAI.  It *contains* Dublin Core elements.
  However, in MARCXML, the out-most element “collection” serves both: It is
  MARCXML proper but also the OAI-PMH “container” of the MARC data.  Is this
  correct, or must it be wrapped in another root element?
- Currently, in the MARC field 450 (abbreviated title) only the DFG project
  number is returned.  There is certainly room for improvement, but how?
- How do we deal with datasets already present in the harvester?  Typically,
  they have been manually enriched, and it would be a pity to lose that data.


Things to improve
-----------------

- “Sprecher” (speaker) and “Leiter” (head) should be distinguished.  They are
  different at least for umbrella projects.
- The returned MARCXML records should contain the URL on GEPRIS of the
  respective project.  This way, we can satisfy the following recommendation of
  the PMH spec: “To facilitate access to the resource associated with harvested
  metadata, repositories should use an element in metadata records to establish
  a linkage between the record (and the identifier of its item) and the
  identifier (URL, URN, DOI, etc.) of the associated resource.”
- An <about> tag in the XML output may be interesting for both denoting the
  origin of the data and the legal status of it.
- The whitelists should be global variables.
