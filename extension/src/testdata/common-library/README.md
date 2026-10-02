# Common Library source fixture

`system-preference.json` is a minimal excerpt of the official EasyEDA Pro
3.2.149 bundled `app.js`, function `uU`, Chinese System `BASE_LIBRARY` defaults.
It retains two public resistor models and the empty `Other` category placeholder.
The HTTP success wrapper is reconstructed to exercise the source adapter.
There are no placement coordinates or geometric units in this catalogue.

The fixture proves only parsing of that source schema. It does not prove that
these old UUIDs resolve on Web V4, that the live endpoint has the same schema,
or that the Common Library M3 device can be placed. M3 entries in unit tests are
synthetic group-selection cases, not a catalogue captured from the editor.

Run from `extension/`:

```sh
node --require ts-node/register --test src/common-library.test.ts
```
