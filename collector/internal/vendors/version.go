package vendors

// ParserVersion identifies transcript parser behavior, not the product build.
// Remote families cache the version that produced their facts, so bump this
// whenever parsing changes which facts a transcript yields: cached families
// then recollect even though their files never changed.
const ParserVersion = "parsers-1"
