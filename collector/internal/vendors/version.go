package vendors

// ParserVersion identifies shared transcript parser behavior, not the product
// build. Bump it when shared parsing changes which facts a transcript yields.
// Vendor-specific changes bump the vendor package's ParserVersion instead;
// remotefacts.ParserVersion joins both, so cached remote families recollect
// even though their files never changed.
const ParserVersion = "parsers-6"
