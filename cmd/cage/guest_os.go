package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/google/uuid"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func writeGuestOSInspection(ctx context.Context, args []string, w io.Writer) error {
	flags := flag.NewFlagSet("guest-os", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "")
	var target uuid.UUID
	flags.Func("vm-id", "", func(value string) error {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || target != uuid.Nil {
			return errors.New("expected one non-empty Hyper-V VM GUID")
		}
		target = id
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*jsonOutput || flags.NArg() != 0 {
		return errors.New("usage: cage guest-os --json [--vm-id GUID]")
	}
	if target == uuid.Nil {
		return json.NewEncoder(w).Encode(guestos.Inspect(ctx))
	}
	result, err := guestos.InspectHyperV(ctx, target)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(result)
}
