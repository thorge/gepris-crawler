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
   it so that we can advertise it here for the benefit of others.  See the
   contact address at the bottom.


Architecture
------------

The GEPRIS crawler consists of two programs, called “crawler” and “pmh”.

crawler
  This is supposed to be run as a cron job.  It updates the local project
  database.

pmh
  This is supposed to be run continuously.  It exposes an OAI-PMH_ interface to
  fetch the projects.

.. _OAI-PMH: http://www.openarchives.org/OAI/openarchivesprotocol.html


The crawler
-----------

You install the crawler by calling
::

   go install jugit.fz-juelich.de/fdm/gepris-crawler/cmd/crawler@latest

Then, you can invoke it with “crawler”.  It takes the following environment
variables:

MONGODB_HOST
  URI to the MongoDB instance.  The GEPRIS crawler creates a database called
  “gepris“ in MongoDB.  MONGODB_HOST defaults to “mongodb://localhost”.

CRAWLER_DEBUG
  If set to “1”, the crawler will cache the fetched JSON responses in the
  directories “``search-results``”, “``project-pages``”, “``person-pages``”,
  “``institution-pages``”, and “``lookups``”, relative to the current working
  directory.  It
  expects these directories to exist.  Whenever it finds a page there, it will
  not fetch it through HTTP.  This way, one can extensively test the crawler
  without hitting the GEPRIS.

CRAWLER_FULL
  By default, only the current calender year and the year before are crawled.
  By setting CRAWLER_FULL to “1”, the whole GEPRIS is downloaded.

CRAWLER_SKIP_ENTITIES
  By default, the persons and institutions participating in the crawled
  projects are downloaded, too.  Each of them is downloaded at most once in 30
  days.  By setting CRAWLER_SKIP_ENTITIES to “1”, this step is skipped.


Data model
----------

The crawler writes four collections into MongoDB: “``projects``”,
“``persons``”, “``institutions``”, and “``lookups``”.  They are linked by the
GEPRIS IDs “``projectId``”, “``personId``”, and “``institutionId``”, which
uniquely identify projects, persons, and institutions.  (GEPRIS does not offer
external identifiers like ORCID or ROR.)

projects
  One document per project, identified by “``identifier``” (e.g.
  “``G:(GEPRIS)327154368``”, used by the PMH server) and “``projectId``” (the
  plain DFG project number).  The fields used by the PMH server (title,
  applicants, speakers, institutions, etc.) are lists of names.  Additionally,
  there are

  - “``personRoles``”: all participating persons with “``personId``”,
    “``name``”, “``role``” (the GEPRIS group key, e.g. “``PSK``” for
    speakers or “``PAN``” for applicants), “``roleLabel``”, “``affiliation``”,
    and “``deceased``”,
  - “``institutionRoles``”: all participating institutions with
    “``institutionId``”, “``name``”, “``role``” (e.g. “``IAN``” for the
    applying institution), and “``roleLabel``”,
  - “``researchSites``”: the institutions where the research takes place
    (“``institutionId``”, “``name``”, “``regionKey``” and “``region``”, i.e.
    the federal state or country, and “``inGermany``”).  This is how GEPRIS
    attributes projects to universities in its search.  Note that individual
    grants (“Sachbeihilfen”) have no “``institutionRoles``”, so this is the
    only way to attribute them to an institution,
  - “``classifications``”: the DFG subject classification on all levels:
    “``discipline``” (e.g. life sciences), “``researchArea``” (“Fachgebiet”,
    e.g. medicine), “``reviewBoard``” (“Fachkollegium”), and
    “``subjectArea``” (“Fach”), each with its key (e.g. “``disciplineKey``”).
    Coordinated programmes are often only classified down to the research
    area.  A project may have several classifications,
  - “``processKey``” and “``mainProcess``”: the DFG process, e.g. “``SFB``”
    (this includes Transregios), “``SBH``” (individual grants), or
    “``FOR``”; “``subProcessKey``” and “``subProcess``”: its variant, e.g.
    “``TRR``” or “``KFO``” (clinical research units), or empty,
  - “``subprojects``”: for umbrella projects, the sub-projects with
    “``projectId``”, “``shortTitle``” (e.g. “``A01``”), title, years, status,
    and “``leaderIds``” (person IDs),
  - “``publications``”: project results with “``publicationId``”,
    “``authors``”, “``title``”, “``year``”, and “``doi``”,
  - “``parentId``”, “``processEn``”, “``titleEn``”,
    “``descriptionEn``”, “``finalReportSummary``”, “``website``”,
    “``internationalRelation``”, and “``isTransferProject``”.

  “``lastModification``” is only touched if data relevant for PMH changes.

persons
  One document per person, identified by “``personId``”, with “``name``”,
  the current institution (“``institutionId``”, “``rootInstitutionId``”, and
  their names), “``prizes``” awarded by the DFG, and “``projects``”, the
  complete list of projects of that person according to GEPRIS, including
  projects not crawled (yet), with “``projectId``”, “``role``”,
  “``processKey``”, “``status``”, and years.

institutions
  One document per institution, identified by “``institutionId``”, with
  name, “``rootInstitutionId``” (e.g. the university of an institute),
  “``type``”, “``sector``”, address, “``dissolved``”, and “``projects``”.
  Note that GEPRIS lists only coordinated programmes there, not individual
  grants.  For all projects of an institution, query “``institutionRoles``” in
  the projects collection.

lookups
  The GEPRIS classifications with German and English names, one document per
  entry with “``table``”, “``key``”, “``name``”, “``nameEn``”, and
  “``parentKey``”.  The tables are “``processes``”, “``subProcesses``”,
  “``disciplines``”, “``researchAreas``”, “``reviewBoards``”,
  “``subjectAreas``”, “``regions``”, and “``researchSites``” (the latter can
  be used to find the institution ID of a university).

The crawler does not store the GEPRIS API responses as such, but only a hash
of them (“``sourceHash``”) to detect changes.  GEPRIS does not publish
funding amounts, so they are not available.

Example queries in the MongoDB shell::

   // All crawled projects of a person
   db.projects.find({"personRoles.personId": 1552179})

   // All persons who were speakers of an SFB, with the number of SFBs
   db.projects.aggregate([
     {$match: {processKey: "SFB"}},
     {$unwind: "$personRoles"},
     {$match: {"personRoles.role": "PSK"}},
     {$group: {_id: "$personRoles.personId", name: {$first: "$personRoles.name"},
               sfbs: {$sum: 1}}}])

   // Institution ID of a university
   db.lookups.find({table: "researchSites", name: /Kiel/})

   // Individual grants starting in 2025 at two universities, per research
   // area
   db.projects.aggregate([
     {$match: {processKey: "SBH", startYear: 2025,
               "researchSites.institutionId": {$in: [10337, 10345]}}},
     {$unwind: "$researchSites"},
     {$match: {"researchSites.institutionId": {$in: [10337, 10345]}}},
     {$project: {site: "$researchSites.name",
                 areas: {$setUnion: ["$classifications.researchArea", []]}}},
     {$unwind: "$areas"},
     {$group: {_id: {site: "$site", area: "$areas"}, projects: {$sum: 1}}},
     {$sort: {"_id.site": 1, projects: -1}}])

   // Number of PIs (leaders of running sub-projects) of SFBs and Transregios
   db.projects.aggregate([
     {$match: {processKey: "SFB", type: "umbrella"}},
     {$project: {pis: {$size: {$setUnion: [{$reduce: {
        input: {$filter: {input: "$subprojects",
                          cond: {$eq: ["$$this.status", "laufend"]}}},
        initialValue: [],
        in: {$concatArrays: ["$$value", "$$this.leaderIds"]}}}, []]}}}},
     {$match: {pis: {$gt: 0}}},
     {$group: {_id: null, average: {$avg: "$pis"},
               min: {$min: "$pis"}, max: {$max: "$pis"}}}])

   // Number of projects per applying institution since 2020
   db.projects.aggregate([
     {$match: {startYear: {$gte: 2020}}},
     {$unwind: "$institutionRoles"},
     {$match: {"institutionRoles.role": "IAN"}},
     {$group: {_id: "$institutionRoles.name", projects: {$sum: 1}}},
     {$sort: {projects: -1}}])


The PMH server
--------------

You install the crawler by calling
::

   go install jugit.fz-juelich.de/fdm/gepris-crawler/cmd/pmh@latest

Then, you can invoke it with “pmh”.  It listens on port 8000.  It takes the
following environment variable:

MONGODB_HOST
  URI to the MongoDB instance.  The PMH server creates a database called
  “gepris“ in MongoDB (which is empty before the first crawler run).
  MONGODB_HOST defaults to “mongodb://localhost”.


Contact
-------

Author and maintainer of this GEPRIS crawler is `Torsten Bronger`_.

.. _Torsten Bronger: mailto:t.bronger@fz-juelich.de
