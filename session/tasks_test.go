package session

import (
	"errors"
	"testing"
)

func TestBatchTasksMarkUnfinishedTasksLostAfterTheBatchEnds(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := store.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []TaskRecord{{Name: "fix", State: TaskRunning}, {Name: "docs", State: TaskDone}} {
		if err := batch.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Save(TaskRecord{Name: "../escape"}); err == nil {
		t.Fatal("saved a task with an invalid name")
	}
	tasks, live, err := store.BatchTasks(batch.ID)
	if err != nil || !live || len(tasks) != 2 || tasks[1].State != TaskRunning {
		t.Fatalf("running batch = %+v, live %v, %v", tasks, live, err)
	}
	batch.Close()
	tasks, live, err = store.BatchTasks(batch.ID)
	if err != nil || live || tasks[0].State != TaskDone || tasks[1].State != TaskLost {
		t.Fatalf("ended batch = %+v, live %v, %v", tasks, live, err)
	}
	var missing *BatchNotFoundError
	if _, _, err := store.BatchTasks("../x"); !errors.As(err, &missing) {
		t.Fatalf("invalid ID: %v", err)
	}
}
