package billing

import (
	"fmt"
	"whatify/backend/internal/models"
	"whatify/backend/pkg/database"
)

// Billing cycles. A plan is always sold monthly; 6-month and 12-month are
// optional and only offered when the admin has set a non-zero total price.
const (
	CycleMonthly    = "mo"
	CycleSemiAnnual = "6mo"
	CycleAnnual     = "12mo"
)

// normalizeCycle maps an incoming cycle string to a known cycle, defaulting to
// monthly for empty/unknown values.
func normalizeCycle(cycle string) string {
	switch cycle {
	case CycleSemiAnnual:
		return CycleSemiAnnual
	case CycleAnnual:
		return CycleAnnual
	default:
		return CycleMonthly
	}
}

// cycleMonths returns the number of months a cycle covers: mo→1, 6mo→6, 12mo→12.
func cycleMonths(cycle string) int {
	switch normalizeCycle(cycle) {
	case CycleSemiAnnual:
		return 6
	case CycleAnnual:
		return 12
	default:
		return 1
	}
}

// resolveCyclePrice loads the plan definition and returns the total price and
// number of months for the requested billing cycle. It errors if the requested
// longer cycle is not offered (price <= 0). Monthly always uses PriceEGP, and
// for monthly it falls back to the legacy IntervalCount/Period so existing
// custom single-period plans keep working.
func resolveCyclePrice(plan models.Plan, cycle string) (amount float64, months int, label string, err error) {
	cycle = normalizeCycle(cycle)

	var planDef models.PlanDef
	if dbErr := database.DB.Where("name = ?", string(plan)).First(&planDef).Error; dbErr != nil {
		// Fallback to hardcoded monthly limits for safety.
		l := GetLimits(plan)
		return l.PriceEGP, 1, l.Label, nil
	}

	switch cycle {
	case CycleSemiAnnual:
		if planDef.Price6moEGP <= 0 {
			return 0, 0, planDef.Label, fmt.Errorf("the 6-month billing cycle is not available for the %s plan", planDef.Label)
		}
		return planDef.Price6moEGP, 6, planDef.Label, nil
	case CycleAnnual:
		if planDef.Price12moEGP <= 0 {
			return 0, 0, planDef.Label, fmt.Errorf("the yearly billing cycle is not available for the %s plan", planDef.Label)
		}
		return planDef.Price12moEGP, 12, planDef.Label, nil
	default:
		// Monthly — honor legacy per-plan Period/IntervalCount for custom plans.
		m := planDef.IntervalCount
		if m < 1 {
			m = 1
		}
		if planDef.Period == "yr" || planDef.Period == "year" {
			m *= 12
		}
		return planDef.PriceEGP, m, planDef.Label, nil
	}
}
