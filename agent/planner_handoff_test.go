package agent

import (
	"testing"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func TestPlannerHandoffTodoRoundTrip(t *testing.T) {
	input := []actool.Todo{
		{Content: "enumerate endpoints", Status: actool.TodoCompleted},
		{Content: "verify candidate", Status: actool.TodoInProgress},
	}
	got := plannerTodosFromDB(plannerTodosToDB(input))
	if len(got) != len(input) {
		t.Fatalf("round trip length=%d want %d", len(got), len(input))
	}
	for i := range input {
		if got[i] != input[i] {
			t.Fatalf("todo %d = %+v want %+v", i, got[i], input[i])
		}
	}
}

func TestPlannerHandoffRejectsUnknownStatusAsPending(t *testing.T) {
	got := plannerTodosFromDB([]db.PlannerHandoffTodo{{Content: "review", Status: "unexpected"}})
	if len(got) != 1 || got[0].Status != actool.TodoPending {
		t.Fatalf("unknown status not downgraded safely: %+v", got)
	}
}
