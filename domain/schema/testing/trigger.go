// Copyright 2026 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package testing

import (
	"fmt"
	"strings"

	domainschema "github.com/juju/juju/domain/schema"
)

// FailingTrigger describes a trigger that aborts the triggering statement.
// Tests use it to exercise rollback paths.
type FailingTrigger struct {
	// Name is the trigger name. On PostgreSQL it also names the trigger
	// function.
	Name string
	// Event is the trigger timing and event, such as "BEFORE INSERT" or
	// "BEFORE UPDATE OF charm_uuid".
	Event string
	// Table is the table the trigger is created on.
	Table string
	// When is an optional condition that may refer to NEW and OLD.
	When string
	// Message is the error message raised by the trigger.
	Message string
}

// FailingTriggerSQL returns the DDL that creates the trigger for the backend
// selected for state tests.
func FailingTriggerSQL(t FailingTrigger) string {
	message := strings.ReplaceAll(t.Message, "'", "''")
	if BackendForTest() == domainschema.BackendPostgreSQL {
		return failingTriggerPostgreSQL(t, message)
	}
	when := ""
	if t.When != "" {
		when = "\nWHEN " + t.When
	}
	return fmt.Sprintf(`
CREATE TRIGGER %s %s ON %s%s
BEGIN
    SELECT RAISE(ABORT, '%s');
END`, t.Name, t.Event, t.Table, when, message)
}

// failingTriggerPostgreSQL evaluates the condition inside the trigger
// function, as PostgreSQL does not allow subqueries in a trigger's WHEN
// clause.
func failingTriggerPostgreSQL(t FailingTrigger, message string) string {
	when := "TRUE"
	if t.When != "" {
		when = t.When
	}
	return fmt.Sprintf(`
CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $fn$
BEGIN
    IF %[4]s THEN
        RAISE EXCEPTION USING MESSAGE = '%[5]s';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END
$fn$;
CREATE TRIGGER %[1]s %[2]s ON %[3]s
FOR EACH ROW EXECUTE FUNCTION %[1]s();`, t.Name, t.Event, t.Table, when, message)
}
