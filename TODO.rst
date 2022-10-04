Todos
=====

A good template for a DFG grant as MARCXML is
<https://juser.fz-juelich.de/record/909442/export/xm>.


Missing output MARC fields
--------------------------

- MARC field 450 (abbreviation)
- MARC field 550 (link to parent)


Things to discuss
-----------------

- Is 024__2 (link to this crawler) okay?
- How do we deal with the mix of institutions and people in MARC field 371?


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
