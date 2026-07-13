// Module benchmarks compares github.com/giraffesyo/pdf against other
// pure-Go PDF text extractors. It is a separate module so its
// dependencies never enter the root module's graph.
module github.com/giraffesyo/pdf/benchmarks

go 1.26.0

require (
	github.com/giraffesyo/pdf v0.0.0
	github.com/ledongthuc/pdf v0.0.0-20250511090121-5959a4027728
	rsc.io/pdf v0.1.1
)

replace github.com/giraffesyo/pdf => ..
