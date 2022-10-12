GEPRIS crawler
==============

Goal
----

Unfortunately, the project database of the German Research Foundation (Deutsche
Forschungsgemeinschaft, DFG) is only available as a browser interface.
Moreover, the DFG does not plan to offer it in reusable form.  Still,
librarians and others need to add metadata for German research projects into
their local systems.

This is where the GEPRIS crawler comes into play.  It makes it possible to
retrieve project metadata in an automatic way.

.. warning::
   Make sure that your crawling does not put unacceptable burden on the GEPRIS
   service.  In particular, choose crawling times when low GEPRIS usage is to
   be expected (e.g. night hours in European time zones), do not fiddle around
   with the rate limiting settings, and don’t call the crawler frequently.
   While debugging, make use of the CRAWLER_DEBUG environment variable, see
   below.

   Before considering setting up an own crawler, see whether you can harvest an
   existing PMH server.  If you run a PMH server, you may want to tell us about
   it so that we can advertise it here for the benefit of others.


Architecture
------------

The GEPRIS crawler consists of two programs, called “crawler” and “pmh”.

crawler
  This is supposed to be run as a cron job.  It updates the local project
  database.

pmh
  This is supposed to be run continuously.  It exposes an OAI-PMH interface to
  fetch the projects.


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

CRAWLER_DEBUG
  If set to “1”, the crawler will cache the fetched HTML pages in the
  directories “``search-results``” and “``project-pages``”, relative to the
  current working directory.  It expects these directories to exist.  Whenever
  it finds a page there, it will not fetch it through HTTP.  This way, one can
  extensively test the crawler without hitting the GEPRIS.

CRAWLER_FULL
  By default, only the current calender year and the year before are crawled.
  By setting CRAWLER_FULL to “1”, the whole GEPRIS is downloaded.


The PMH server
--------------

You install the crawler by calling
::

   go install jugit.fz-juelich.de/fdm/gepris-crawler/cmd/pmh

from the root directory of the source code.  Then, you can invoke it with
“pmh”.  It listens on port 8000.  It takes the following environment variable:

MONGODB_HOST
  URI to the MongoDB instance.  The PMH server – as the crawler –  creates a
  database called “gepris“ in MongoDB.  MONGODB_HOST defaults to
  “mongodb://localhost”.
