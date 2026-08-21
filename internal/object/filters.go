package object

import (
	"fmt"
	"io"

	"github.com/giraffesyo/pdf/internal/filter"
)

// applyFilters wraps body with the stream's decode chain from /Filter and
// /DecodeParms (each of which may be a single value or an array). The
// returned release function hands pooled decoder state back once the
// reader is no longer used; it is nil when the chain holds none.
func (r *Reader) applyFilters(body io.Reader, d dict) (io.Reader, func(), error) {
	filters := filterNames(d)
	if len(filters) == 0 {
		return body, nil, nil
	}
	if len(filters) > maxFilterChain {
		return nil, nil, fmt.Errorf("pdf: filter chain of %d exceeds limit", len(filters))
	}
	parms := decodeParms(r, d, len(filters))
	rd := body
	var releasers []filter.Releaser
	release := func() {
		for _, rel := range releasers {
			rel.Release()
		}
	}
	for i, name := range filters {
		var err error
		rd, err = filter.Apply(rd, name, parms[i])
		if err != nil {
			release()
			return nil, nil, err
		}
		if rel, ok := rd.(filter.Releaser); ok {
			releasers = append(releasers, rel)
		}
	}
	if len(releasers) == 0 {
		return rd, nil, nil
	}
	return rd, release, nil
}

// filterNames returns the /Filter (or abbreviated /F) entries in order.
func filterNames(d dict) []string {
	v, ok := d["Filter"]
	if !ok {
		v, ok = d["F"]
		if !ok {
			return nil
		}
	}
	switch x := v.(type) {
	case name:
		return []string{string(x)}
	case []Value:
		names := make([]string, 0, len(x))
		for _, e := range x {
			names = append(names, e.Name())
		}
		return names
	default:
		return nil
	}
}

// decodeParms returns per-filter parameters aligned with the filter list.
func decodeParms(r *Reader, d dict, n int) []filter.Params {
	out := make([]filter.Params, n)
	pv, ok := d["DecodeParms"]
	if !ok {
		pv = d["DP"]
	}
	switch x := pv.(type) {
	case dict:
		if n > 0 {
			out[0] = paramsFromDict(r, x)
		}
	case []Value:
		for i := 0; i < n && i < len(x); i++ {
			if pd, ok := x[i].resolve().data.(dict); ok {
				out[i] = paramsFromDict(r, pd)
			}
		}
	}
	return out
}

func paramsFromDict(r *Reader, d dict) filter.Params {
	p := filter.Params{
		Predictor:        int(r.asInt(d["Predictor"])),
		Colors:           int(r.asInt(d["Colors"])),
		BitsPerComponent: int(r.asInt(d["BitsPerComponent"])),
		Columns:          int(r.asInt(d["Columns"])),
	}
	if ec, ok := d["EarlyChange"].(int64); ok && ec == 0 {
		p.NoEarlyChange = true
	}
	return p
}
