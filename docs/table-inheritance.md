# Experimental table inheritance

This fork implements a bounded subset of PostgreSQL table inheritance. It is
intended for compatibility experiments with fresh synthetic databases. It is not
complete PostgreSQL inheritance support or a production-readiness statement.

## Reads and metadata

`CREATE TABLE child (...) INHERITS (parent)` records an ordered, schema-qualified
parent relationship in the database root. Ordinary parent SELECTs scan the parent
and its descendants and project the parent's columns. A descendant reached by
more than one inheritance path is scanned once. `SELECT ... FROM ONLY parent`
reads only the named relation. Inserts target only the named relation.

Parent scans do not expose the parent's indexes or primary-key uniqueness to the
optimizer: different descendants can contain the same key. Consequently these
scans may be slower than physical table reads. Permissions are checked on the
relation named in the query, matching PostgreSQL's inheritance permission model.
Direct child queries still require permission on the child.

The relationship collection is versioned with the database root. It participates
in transaction rollback, savepoints and Dolt branches. `pg_catalog.pg_inherits`
reports direct relationships in declared parent order.

Tables created by older versions of this fork used schema copying without
relationship metadata. This change cannot recover their original parent lists;
recreate experimental databases from their original schema definitions. The root
format gains a field; older binaries are not a supported downgrade path for roots
written by this feature. Keep a pre-trial backup when comparing versions.

## Schema changes and explicit limits

Supported nullable ADD COLUMN operations propagate to descendants after checking
all descendants for missing tables or name collisions. Supported types are int2,
int4, int8, text, varchar (including length), jsonb and timestamp. Adding defaults,
required columns, generated columns or a column position to a parent is rejected.
A collision with a descendant's existing column is rejected, rather than merged.
`SET NOT NULL` is supported on a parent or intermediate child and propagates to
all of its descendants after every affected physical table has been checked for
NULL values. A child may also strengthen an inherited nullable column. A failed
preflight leaves every affected schema unchanged.

Recursive UPDATE, DELETE and TRUNCATE are not implemented. The engine rejects
these operations on parents with descendants. UPDATE ONLY, DELETE ONLY and
TRUNCATE ONLY are also not implemented. Ordinary updates to a leaf table work.

Table rename/drop and inherited-column rename/drop/type changes are guarded.
`DROP NOT NULL` is rejected when a column participates in inheritance because
column ownership is not yet stored. A leaf's own additional columns can still be
changed, including its locally owned nullability. Parent default and CHECK
changes are rejected. ALTER TABLE ONLY supports the existing physical
primary/unique/foreign-key operations and guarded default changes; other forms
are rejected. Temporary inheritance, cross-database inheritance and historical
inherited scans are outside the supported subset.

These limits are intentional errors, not emulation of successful PostgreSQL
operations. Additional PostgreSQL inheritance details, including catalog column
inheritance counts and system-column semantics, require separate work.

## Application validation

A database merge does not execute application methods. Even a conflict-free merge
needs application-level checks for stored computations, references, sequences,
permissions, schema/code alignment and external file storage. Successful synthetic
SQL tests do not establish that an application can safely use this backend.

Reference: [PostgreSQL 16 inheritance documentation](https://www.postgresql.org/docs/16/ddl-inherit.html).
