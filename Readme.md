[![Go reference documentation](https://img.shields.io/badge/doc-go%20reference-73FA79)](https://pkg.go.dev/github.com/boxesandglue/baseline-pdf)&nbsp;[![Homepage](https://img.shields.io/badge/homepage-boxesandglue.dev-blue)](https://boxesandglue.dev)&nbsp;[![Explore in Constellation](https://img.shields.io/badge/Explore%20in-Constellation-blue)](https://constellation.speedata.de/#baseline-pdf)


![baseline logo](https://user-images.githubusercontent.com/209434/228155279-35b5dcd2-e00d-442c-abae-3687e2721aa3.png)


baseline-pdf is a low-level PDF writer for the Go language. It is used in the [boxes and glue typesetting library](https://boxesandglue.dev) but can be used in other projects as well.

Fonts are parsed, shaped and subset with [textshape](https://github.com/boxesandglue/textshape), including variable fonts, which are converted to static instances. Pages of existing PDF files can be imported through [gofpdi](https://github.com/boxesandglue/gofpdi).

# Installation

```bash
go get github.com/boxesandglue/baseline-pdf
```

# Documentation

This library has a [godoc reference](https://pkg.go.dev/github.com/boxesandglue/baseline-pdf) and a [more verbose manual](https://boxesandglue.dev/baseline).

# Ecosystem

baseline-pdf is part of a broader ecosystem of PDF, typesetting and publishing technologies.

**[Explore the constellation →](https://constellation.speedata.de/#baseline-pdf)**

# License

BSD license - see License.md

# Contact

Patrick Gundlach, <gundlach@speedata.de>

