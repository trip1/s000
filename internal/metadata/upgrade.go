package metadata

import "fmt"

// UpgradePath returns migrations needed to upgrade from fromVersion to latest.
func UpgradePath(backend Backend, fromVersion int) ([]Migration, error) {
	plan, err := MigrationPlan(backend)
	if err != nil {
		return nil, err
	}
	if len(plan) == 0 {
		return nil, fmt.Errorf("no migration plan for backend %q", backend)
	}

	latest := plan[len(plan)-1].Version
	if fromVersion > latest {
		return nil, fmt.Errorf("from version %d is newer than latest %d", fromVersion, latest)
	}

	out := make([]Migration, 0, len(plan))
	for _, m := range plan {
		if m.Version > fromVersion {
			out = append(out, m)
		}
	}
	return out, nil
}
