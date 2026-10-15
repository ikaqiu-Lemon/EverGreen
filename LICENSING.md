# Repository licensing

This repository contains components under different licenses.

| Path | Component | License |
| --- | --- | --- |
| repository root, excluding separately licensed subtrees | Evergreen core and CLI | Apache License 2.0 (`LICENSE`) |
| `siyuan/` | Embedded and modified SiYuan frontend, kernel, and runtime | GNU Affero General Public License v3 or later (`siyuan/LICENSE`) |
| bundled dependencies and assets | Third-party software | See `siyuan/THIRD_PARTY_NOTICES.md` and license files beside the relevant assets |

Moving SiYuan into this repository does not relicense it under Apache-2.0.
Running or distributing the combined application must satisfy the applicable
AGPL corresponding-source obligations and retain all notices.

The imported snapshot provenance is recorded in
`siyuan/.evergreen-source.json`. The matching source for a build is the same
Evergreen revision, including both the repository root and `siyuan/`; an
upstream SiYuan tag is not a substitute for the modified combined source.
