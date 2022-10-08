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


Missing output MARC fields
--------------------------

- MARC field 450 (abbreviation)


Things to discuss
-----------------

- Is 024__2 (link to this crawler) okay?
- How do we deal with the mix of institutions and people in MARC field 371?  In
  particular, may also be an institution in field 371?


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
  one can use Go structs for MongoDB’s inserting, updating an replacing.  One
  just has got to tag the fields in the type declarations.  I should use that
  instead of the manual mapping to a bson data structure, which is error prone.
  I have to check, however, whether this leads to wrong upper/lowercase in the
  MongoDB document.
