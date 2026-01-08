[![Go reference documentation](https://img.shields.io/badge/doc-go%20reference-73FA79)](https://pkg.go.dev/github.com/boxesandglue/baseline-pdf)&nbsp;[![Homepage](https://img.shields.io/badge/homepage-boxesandglue.dev-blue)](https://boxesandglue.dev)


![baseline logo](https://user-images.githubusercontent.com/209434/228155279-35b5dcd2-e00d-442c-abae-3687e2721aa3.png)


baseline-pdf is a low-level PDF writer for the Go language. It is used in the [boxes and glue typesetting library](https://boxesandglue.dev) but can be used in other projects as well.

This library has a [godoc reference](https://pkg.go.dev/github.com/boxesandglue/baseline-pdf) and a [more verbose manual](https://boxesandglue.dev/baseline).

# Version 1.1

Starting with version 1.1, baseline-pdf uses [textshape](https://github.com/boxesandglue/textshape) for font handling (parsing, shaping, subsetting). This replaces the previous dependency on [textlayout](https://github.com/boxesandglue/textlayout), which is now obsolete.

New features in 1.1:
- Variable font support with instancing (convert variable fonts to static instances)
- Improved font subsetting

# Status

Not yet used in production. Expect API changes.

# License

BSD license - see License.md

# Contact

Patrick Gundlach, <gundlach@speedata.de>

