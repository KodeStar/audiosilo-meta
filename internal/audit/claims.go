package audit

// settleContestedClaims collects mechanical claimants before downgrading any of
// them, so the result does not depend on which proposal is visited first. keys
// identifies the places a finding claims; settle preserves each detector's wording.
// An already advisory finding can still name the mechanical rivals at its place.
func settleContestedClaims(f *findings, keys func(Proposal) []string, settle func(*Finding, []string)) {
	claims := map[string][]string{}
	rows := map[string][]int{}
	for i, r := range f.rows {
		for _, key := range keys(r.Propose) {
			rows[key] = append(rows[key], i)
			if !r.Propose.Advisory {
				claims[key] = append(claims[key], r.Key)
			}
		}
	}
	for _, key := range sortedKeys(claims) {
		if len(claims[key]) < 2 {
			continue
		}
		for _, i := range rows[key] {
			f.rows[i].Propose.Advisory = true
			settle(&f.rows[i], claims[key])
		}
	}
}
