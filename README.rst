GEPRIS crawler
==============

Goal
----

Unfortunately, the project database of the German Reserach Foundation (Deutsche
Forschungsgemeinschaft, DFG) is only available as a Web interface.  Moreover,
the DFG does not plan to offer it in reusable form.  Still, librarians and
others need to add metadata for German reserach projects into their local
systems.

This is where the GEPRIS crawler comes into play.  It makes it possible to
retrieve project metadata in an automatic way.


Architecture
------------

The GEPRIS crawler consists of two programs, called “crawler” and “pmh”.

crawler
  This is supposed to be run as a cron job.  It updates the local project
  database.

pmh
  This is supposed to be run continuously.  It exposes a PMH interface to fetch
  the projects.


The crawler
-----------

You install the crawler by calling

::
   go install jugit.fz-juelich.de/fdm/gepris-crawler/cmd/crawler

from the root directory of the source code.  Then, you can invoke it with
“crawler”.  It takes the following environment variables:

MONGODB_HOST
  URI to the MongoDB instance.  The GEPRIS crawler – this includes the PMH
  server – creates a database called “gepris“ in MongoDB.  MONGODB_HOST
  defaults to “mongodb://localhost”.
