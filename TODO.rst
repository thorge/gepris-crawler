Todos
=====

A good template for a DFG grant as MARCXML is
<https://juser.fz-juelich.de/record/909442/export/xm>.


- lastModification is useless currently as all records are overwritten
  unconditionally.  Instead, I will have to read the project from MongoDB and
  compare it with what I found in GEPRIS.  If different (excluding the
  lastModification field of course), the record needs to be updated.
  Unfortunately, the comparison has to be field-by-field.  I know of no
  existing function.


Things to discuss
-----------------

- Is 024__2 (link to this crawler) okay?
- How do we deal with the mix of institutions and people in MARC field 371?  In
  particular, may also be an institution in field 371?
- Is the out-most element “collection” in the MARCXML output correct?  The
  Dublin Code output in the PMH spec contains an out-most element that is *not*
  part of Dublin Core but of OAI.  In *contains* Dublin Core elements.
  However, in MARCXML, the out-most element “collection” serves both: It is
  MARCXML proper but also the OAI-PMH “container” of the MARC data.  Is this
  correct, or must it be wrapped in another root element?
- Currently, in the MARC field 450 (abbreviated title) only the DFG project
  number is returned.  There is certainly room for improvement, but how?
- How do we deal with datasets already present in the harvester?  Typically,
  they have been manually enriched, and it would be a pity to lose that data.


Things to improve
-----------------

- The handler functions for the PMH verbs should be Go routines that report
  their records back through a channel.  The caller can then write the XML
  incrementally to the response variable.  Benefits: Much less memory
  consumption, response can start immediately.
- When iterating over the MongoDB results, expiring of the context should be
  detected.
- Instead of upserting in MongoDB using the project number as the filter, the
  project number should be in the ``_id`` field.  Then, I don’t need the index
  of “number” any more.  Moreover, it *may* be faster.
- “Sprecher” (speaker) and “Leiter” (head) should be distinguished.  They are
  different at least for umbrella projects.
- According to
  <https://betterprogramming.pub/how-to-use-golang-structs-with-mongodb-f1772e4a1da3#4cf5>,
  one can use Go structs for MongoDB’s inserting, updating, and replacing.  One
  just has got to tag the fields in the type declarations.  I should use that
  instead of the manual mapping to a bson data structure, which is error prone.
  I have to check, however, whether this leads to wrong upper/lowercase in the
  MongoDB document.
- The returned records should contain the URL on GEPRIS of the respective
  project.  This way, we can satisfy the following recommendation of the PMH
  spec: “To facilitate access to the resource associated with harvested
  metadata, repositories should use an element in metadata records to establish
  a linkage between the record (and the identifier of its item) and the
  identifier (URL, URN, DOI, etc.) of the associated resource.”
- An <about> tag in the XML output may be interesting for both denoting the
  origin of the data and the legal status of it.
- grantEntryOnlyHeader may be removed and grantEntry used instead with the
  “metadata” field marked as “omitempty”.
- Get rid of buildGrantOnlyHeader.
- Revisit error value creation and dealing with error values.  At the same
  time, enrich the doc comments with how errors are returned.
- MARCXML embedding into PMH should be done according to
  <http://www.openarchives.org/OAI/2.0/guidelines-marcxml.htm>.  In particular,
  there is no ``<collection>``.
- Get rid of AmbiguousArgsError.  Instead, an errorData should be returned.
- Many structs passed by value should be passed as pointers.
- The whitelists should be global variables
