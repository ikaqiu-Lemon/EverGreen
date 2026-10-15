# Corresponding Source for Evergreen's Embedded SiYuan

This SiYuan frontend, kernel, runtime, and build tooling are maintained as the
`siyuan/` subtree of the Evergreen repository. SiYuan is distributed under the
GNU Affero General Public License, version 3 or later.

The complete corresponding source is the same Evergreen revision that produced
the local build or package. It includes:

- this `siyuan/` subtree;
- the Evergreen module at the repository root;
- `go.work`, dependency manifests, and build scripts; and
- the original licenses and third-party notices.

The historical SiYuan snapshot commit and tree are recorded in
`.evergreen-source.json`. An upstream SiYuan tag or release does not represent
the additional Evergreen integration changes and must not be used as the
corresponding source for this combined work.

This development branch does not publish an official binary release. A future
Evergreen release must ship or link an exact source archive generated from the
same Evergreen release commit as its binary artifacts and list both in that
release's checksums and provenance.
