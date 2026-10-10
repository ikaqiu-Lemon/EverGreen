package cli

import (
	"context"

	"github.com/ikaqiu-Lemon/EverGreen/internal/client"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func (r *Root) applySYOperation(inv *Invocation, online *client.Client, operation core.PlannedOperation) (*Result, error) {
	ctx := context.Background()
	var record core.OperationRecord
	var err error
	if online != nil {
		record, err = online.Apply(ctx, core.ApplyRequest{Operation: operation})
	} else {
		principal, principalErr := syPrincipal(inv)
		if principalErr != nil {
			return nil, syError(principalErr)
		}
		runtime, openErr := txn.OpenOfflineSYRuntime(ctx, txn.OfflineSYRuntimeOptions{
			Root: inv.VaultFlag, Holder: "eg-cli", Principal: principal,
			Registry: core.ApplicationRegistry(), Authorizer: core.CommandPolicy{},
		})
		if openErr != nil {
			return nil, syError(openErr)
		}
		defer runtime.Close()
		record, err = runtime.Apply(ctx, core.ApplyRequest{Operation: operation})
	}
	result := &Result{Data: map[string]interface{}{"operation": record}}
	if err != nil {
		return result, syError(err)
	}
	return result, nil
}
