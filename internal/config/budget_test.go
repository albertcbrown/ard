package config

import "testing"

func TestRoleBudgetUSDPerDay(t *testing.T) {
	r := &Resolved{BudgetUSDPerDay: 50}
	if c, ok := r.RoleBudgetUSDPerDay(Role{BudgetUSDPerDay: 7}); !ok || c != 7 {
		t.Errorf("override: %v %v", c, ok)
	}
	if c, ok := r.RoleBudgetUSDPerDay(Role{}); ok || c != 0 {
		t.Errorf("unset: %v %v", c, ok)
	}
	if _, ok := r.RoleBudgetUSDPerDay(Role{BudgetUSDPerDay: -1}); ok {
		t.Error("negative override must count as unset")
	}
}
