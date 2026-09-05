package spec

// Reachable returns every named schema reachable from a document's operations,
// in a deterministic order: document order of first encounter.
//
// This is what makes a per-service spec self-contained without dragging in the
// whole shared library. A shared root defining fifty components contributes
// only the ones a service's operations actually touch.
func Reachable(d *Document) []*Schema {
	c := &collector{seen: map[*Schema]bool{}}
	for _, pi := range d.Paths {
		for _, op := range pi.Operations {
			for _, p := range op.Parameters {
				c.add(p.Schema)
			}
			if op.RequestBody != nil {
				for _, m := range op.RequestBody.Content {
					c.add(m.Schema)
				}
			}
			for _, r := range op.Responses {
				for _, h := range r.Headers {
					c.add(h.Schema)
				}
				for _, m := range r.Content {
					c.add(m.Schema)
				}
			}
		}
	}
	return c.named
}

// ReachableFrom returns every named schema reachable from the given roots,
// including the roots themselves.
func ReachableFrom(roots []*Schema) []*Schema {
	c := &collector{seen: map[*Schema]bool{}}
	for _, s := range roots {
		c.add(s)
	}
	return c.named
}

type collector struct {
	seen  map[*Schema]bool
	named []*Schema
}

func (c *collector) add(s *Schema) {
	if s == nil || c.seen[s] {
		return
	}
	// Record before descending so a recursive schema is listed once, at the
	// point it was first reached.
	c.seen[s] = true
	if s.Named() {
		c.named = append(c.named, s)
	}
	for _, p := range s.Properties {
		c.add(p.Schema)
	}
	c.add(s.Items)
	c.add(s.AdditionalPro)
	c.add(s.Not)
	for _, list := range [][]*Schema{s.AllOf, s.OneOf, s.AnyOf} {
		for _, sub := range list {
			c.add(sub)
		}
	}
	if s.Discriminator != nil {
		for _, e := range s.Discriminator.Mapping {
			c.add(e.Schema)
		}
	}
}
