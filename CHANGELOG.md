# Changelog

## [0.8.0](https://github.com/giraffesyo/pdf/compare/v0.7.0...v0.8.0) (2026-09-25)


### Features

* decode JPEG 2000 images ([#67](https://github.com/giraffesyo/pdf/issues/67)) ([beacffd](https://github.com/giraffesyo/pdf/commit/beacffd73a61cd04f35a308d0529cf7b91fca2fb))
* read Adobe CJK collections' CIDs through built-in tables ([#56](https://github.com/giraffesyo/pdf/issues/56)) ([dd67413](https://github.com/giraffesyo/pdf/commit/dd6741323a6700a446bca60d4b678bb7a7770d66))
* read side-by-side columns in order by default ([#47](https://github.com/giraffesyo/pdf/issues/47)) ([eb75197](https://github.com/giraffesyo/pdf/commit/eb751972c199f07b248a2d4427d80f1d9b748a73))
* read the predefined Unicode CMaps without a resolver ([#51](https://github.com/giraffesyo/pdf/issues/51)) ([358401a](https://github.com/giraffesyo/pdf/commit/358401a37a2b059236ce10007c1f5d725a58b907))


### Bug Fixes

* break words at poppler's 0.1 em gap, and keep kerned-over spaces ([#52](https://github.com/giraffesyo/pdf/issues/52)) ([1ea99b1](https://github.com/giraffesyo/pdf/commit/1ea99b11dc212b4d3b274bd25605df6492ebb744))
* decode spot-colour palettes and CMYK JPEGs without an Adobe marker ([#66](https://github.com/giraffesyo/pdf/issues/66)) ([7503ec7](https://github.com/giraffesyo/pdf/commit/7503ec7a33c4201e3509103385618c599b6d3e7b))
* derive R6 keys to the spec, and recover checksum and page-tree damage ([#50](https://github.com/giraffesyo/pdf/issues/50)) ([09f3b58](https://github.com/giraffesyo/pdf/commit/09f3b58ab8e73d18adea8472f99ce68e9bc487e9))
* draw a Form XObject that draws itself only once ([#55](https://github.com/giraffesyo/pdf/issues/55)) ([0caf7e4](https://github.com/giraffesyo/pdf/commit/0caf7e4c392aa749e6a321192b4d25f7eed450b2))
* draw checkbox and radio marks, and the fonts forms name without supplying ([#57](https://github.com/giraffesyo/pdf/issues/57)) ([b0cc441](https://github.com/giraffesyo/pdf/commit/b0cc441a1e0b8c5162fd46a2c853a084d19ece78))
* keep concurrent page workers off each other's readers ([#61](https://github.com/giraffesyo/pdf/issues/61)) ([23b0531](https://github.com/giraffesyo/pdf/commit/23b05312147026b19c0dff18718e80928d331fc2))
* keep superscripts and subscripts on their line ([#59](https://github.com/giraffesyo/pdf/issues/59)) ([601d667](https://github.com/giraffesyo/pdf/commit/601d667ae7884273010558ad4fe3f29939cee982))
* leave off-page text out of Page.Text, and mask password fields ([#53](https://github.com/giraffesyo/pdf/issues/53)) ([a4e4be3](https://github.com/giraffesyo/pdf/commit/a4e4be31664f9a470761a395a22aeeff19808ff9))
* measure a glyph without its spacing, and join unspaced scripts ([#65](https://github.com/giraffesyo/pdf/issues/65)) ([43c529b](https://github.com/giraffesyo/pdf/commit/43c529ba2179f2fb05007a243255443e407f233a))
* read CJK fonts' roman CIDs and a ToUnicode named Identity-H ([#54](https://github.com/giraffesyo/pdf/issues/54)) ([2554e67](https://github.com/giraffesyo/pdf/commit/2554e6710afd6584171b91a872e3f1b93b10ab4d))
* read Type3 fonts by their FontMatrix and glyph names ([#49](https://github.com/giraffesyo/pdf/issues/49)) ([1d7b40b](https://github.com/giraffesyo/pdf/commit/1d7b40be98d0ffe95e6ac496ce9d75e3f38906ce))
* show a list box's options from where it is scrolled ([#58](https://github.com/giraffesyo/pdf/issues/58)) ([c02e3c5](https://github.com/giraffesyo/pdf/commit/c02e3c58b30fbc857b020a1d3af1c494484da178))
* what a real-world sample (GovDocs1) found ([#62](https://github.com/giraffesyo/pdf/issues/62)) ([dfa9668](https://github.com/giraffesyo/pdf/commit/dfa9668ee4a6aad9ca8ac8983861245569359f28))


### Performance

* give back the allocations the new layout added ([#63](https://github.com/giraffesyo/pdf/issues/63)) ([3fea822](https://github.com/giraffesyo/pdf/commit/3fea822e78f2a41191c951d063f14e1867b1ce60))

## [0.7.0](https://github.com/giraffesyo/pdf/compare/v0.6.0...v0.7.0) (2026-09-24)


### Features

* include annotation and form-field text in page text ([#43](https://github.com/giraffesyo/pdf/issues/43)) ([5ae181b](https://github.com/giraffesyo/pdf/commit/5ae181b09112c17e55ce1dc0a569433fab44ef71))


### Bug Fixes

* drop lines that hold only space glyphs ([#45](https://github.com/giraffesyo/pdf/issues/45)) ([773bf8a](https://github.com/giraffesyo/pdf/commit/773bf8ab191101e79a4777c12ced97cef2777d90))
* grow the object parse window when an object outruns it ([#38](https://github.com/giraffesyo/pdf/issues/38)) ([91cc762](https://github.com/giraffesyo/pdf/commit/91cc7626f4cd7df7cf18e269c505d783f23beb45))
* open documents that encrypt only their attachments ([#41](https://github.com/giraffesyo/pdf/issues/41)) ([0335d6f](https://github.com/giraffesyo/pdf/commit/0335d6f4dc45fbc92125886f7b5b10736b319ecb))
* read repainted text once and keep overlaid runs apart ([#39](https://github.com/giraffesyo/pdf/issues/39)) ([230b372](https://github.com/giraffesyo/pdf/commit/230b3729f0cc5d6860351e6e1b6f2f417b574619))
* read right-to-left text in logical order ([#40](https://github.com/giraffesyo/pdf/issues/40)) ([4dcb815](https://github.com/giraffesyo/pdf/commit/4dcb8151ded9b62406ffd9df007cb4275b968bcf))
* recover page trees behind a misleading cross-reference table ([#42](https://github.com/giraffesyo/pdf/issues/42)) ([fb3d7e3](https://github.com/giraffesyo/pdf/commit/fb3d7e3803caa3338a40a9d01e8387f1343f8af8))

## [0.6.0](https://github.com/giraffesyo/pdf/compare/v0.5.0...v0.6.0) (2026-08-21)


### Features

* report a page's image count, and take a predicate for OCR selection ([#33](https://github.com/giraffesyo/pdf/issues/33)) ([7a48b44](https://github.com/giraffesyo/pdf/commit/7a48b444fbf33c870e236da759d1722ef646bbe2))

## [0.5.0](https://github.com/giraffesyo/pdf/compare/v0.4.0...v0.5.0) (2026-08-21)


### ⚠ BREAKING CHANGES

* report page images, decode CCITT/JBIG2, and hand OCR the page's images ([#30](https://github.com/giraffesyo/pdf/issues/30))

### Features

* report page images, decode CCITT/JBIG2, and hand OCR the page's images ([#30](https://github.com/giraffesyo/pdf/issues/30)) ([77aaa5d](https://github.com/giraffesyo/pdf/commit/77aaa5d3029604078d8436c24210bd3a078df37f))


### Bug Fixes

* bound the object numbers a cross-reference rebuild trusts ([#32](https://github.com/giraffesyo/pdf/issues/32)) ([c5515a7](https://github.com/giraffesyo/pdf/commit/c5515a73561457fbbed98bab5dcdc3ba20f0da07))

## [0.4.0](https://github.com/giraffesyo/pdf/compare/v0.3.0...v0.4.0) (2026-08-21)


### ⚠ BREAKING CHANGES

* derive glyph baseline and quad instead of storing them ([#17](https://github.com/giraffesyo/pdf/issues/17))

### Performance

* cache fonts per document and cut layout and lexer allocation ([#14](https://github.com/giraffesyo/pdf/issues/14)) ([26f36cc](https://github.com/giraffesyo/pdf/commit/26f36cc1a79a44ee1a35bcd5c7a814036d9d2f11))
* derive glyph baseline and quad instead of storing them ([#17](https://github.com/giraffesyo/pdf/issues/17)) ([a0a78e9](https://github.com/giraffesyo/pdf/commit/a0a78e945442ffb135b0efe253d5a6f3545f968d))
* extract and lay out pages concurrently ([#27](https://github.com/giraffesyo/pdf/issues/27)) ([a1b6850](https://github.com/giraffesyo/pdf/commit/a1b6850734e72c654a22914fe32d38cace740eac))
* parse embedded font programs on first use and reuse the object parse window ([#20](https://github.com/giraffesyo/pdf/issues/20)) ([ffe0d4a](https://github.com/giraffesyo/pdf/commit/ffe0d4aac878b4a1b4961ba5e901e9412c9a9cb6))
* reuse page-walk buffers across a document and size the xref table on demand ([#24](https://github.com/giraffesyo/pdf/issues/24)) ([75019bd](https://github.com/giraffesyo/pdf/commit/75019bdd1af3d93e10fe44d41e91965a7d025f2c))
* skip empty layout buckets, pool Flate decompressors, grow stream buffers gently ([#22](https://github.com/giraffesyo/pdf/issues/22)) ([207cc88](https://github.com/giraffesyo/pdf/commit/207cc887c709af2a0d672f4f78ca117e7982bda6))
* skip the line search for same-baseline glyphs, the sort for ordered lines, and repeated simple-font decoding ([#23](https://github.com/giraffesyo/pdf/issues/23)) ([fa9719a](https://github.com/giraffesyo/pdf/commit/fa9719ae8571975373b32b38aab9add57499bba0))

## [0.3.0](https://github.com/giraffesyo/pdf/compare/v0.2.1...v0.3.0) (2026-08-21)


### Features

* complete PDF extraction capabilities ([#11](https://github.com/giraffesyo/pdf/issues/11)) ([1c8295e](https://github.com/giraffesyo/pdf/commit/1c8295e874b9f36bed186235aa18d0f76a28f929))


### Bug Fixes

* fold ligature presentation forms to letter sequences ([#10](https://github.com/giraffesyo/pdf/issues/10)) ([44a3964](https://github.com/giraffesyo/pdf/commit/44a396425d41cd564ab084272e041e2001f4a170))
