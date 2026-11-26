package setupcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
)

const botLoginVar = "ARD_BOT_LOGIN"

// VariableWriter writes repository variables with the operator's token. The
// effect layer implements it.
type VariableWriter interface {
	SetRepoVariable(ctx context.Context, repo, name, value string) error
}

// botLogin is the state of the ARD_BOT_LOGIN check.
type botLogin struct {
	row     Row
	want    string // the App's login; empty when it cannot be derived
	replace bool   // a different value exists and needs confirmation
}

func (d Deps) setCmd(want string) string {
	return fmt.Sprintf("gh variable set %s -R %s --body '%s'", botLoginVar, d.Repo, want)
}

// checkBotLogin compares ARD_BOT_LOGIN with the App's login. The wanted
// value comes only from the authenticated identity, never from config.
func checkBotLogin(ctx context.Context, d Deps) (botLogin, error) {
	manual := func(detail string) botLogin {
		return botLogin{row: Row{Name: botLoginVar, Status: StatusManual, Detail: detail}}
	}
	id, err := d.Policy.Identity(ctx)
	if err != nil {
		return botLogin{}, fmt.Errorf("setup: identity: %w", err)
	}
	if !strings.HasSuffix(id.Login, "[bot]") || id.Login == "[bot]" {
		return manual("ard is not running as a GitHub App; " + botLoginVar + " needs the App's <slug>[bot] login"), nil
	}
	want := id.Login
	have, found, err := d.Policy.GetVariable(ctx, botLoginVar)
	if err != nil {
		if errors.Is(err, github.ErrForbidden) || errors.Is(err, github.ErrNotFound) {
			return manual("cannot read the variable; run: " + d.setCmd(want)), nil
		}
		return botLogin{}, fmt.Errorf("setup: read %s: %w", botLoginVar, err)
	}
	have = strings.TrimSpace(have)
	switch {
	case !found || have == "":
		return botLogin{row: Row{Name: botLoginVar, Status: StatusDrift, Detail: "not set; will set to " + want}, want: want}, nil
	case have == want:
		return botLogin{row: Row{Name: botLoginVar, Status: StatusOK, Detail: want}, want: want}, nil
	}
	return botLogin{row: Row{Name: botLoginVar, Status: StatusDrift, Detail: fmt.Sprintf("is %s, App is %s", have, want)}, want: want, replace: true}, nil
}

// applyBotLogin writes the variable. A different existing value is replaced
// only when Confirm says yes. Failures become manual rows, never aborts.
func applyBotLogin(ctx context.Context, d Deps, b botLogin) Row {
	manual := func(why string) Row {
		return Row{Name: botLoginVar, Status: StatusManual, Detail: why + "; run: " + d.setCmd(b.want)}
	}
	if d.Variables == nil {
		return manual("no operator token available")
	}
	if b.replace {
		ok := false
		if d.Confirm != nil {
			var err error
			ok, err = d.Confirm(fmt.Sprintf("%s on %s is set to a different value (%s). Replace it with the App's login?", botLoginVar, d.Repo, b.row.Detail))
			if err != nil {
				ok = false
			}
		}
		if !ok {
			return manual(b.row.Detail + "; not replaced without confirmation")
		}
	}
	if err := d.Variables.SetRepoVariable(ctx, d.Repo.String(), botLoginVar, b.want); err != nil {
		if errors.Is(err, effects.ErrNoOperatorToken) {
			return manual("no operator token available (set ARD_GITHUB_TOKEN or run gh auth login)")
		}
		return manual("could not set the variable")
	}
	r := Row{Name: botLoginVar, Status: StatusChanged, Detail: "set to " + b.want}
	if d.DryRun {
		r.Detail = "would set to " + b.want
	}
	return r
}
