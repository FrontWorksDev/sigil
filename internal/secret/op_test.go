package secret

import (
	"reflect"
	"testing"
)

func TestHasOpRef(t *testing.T) {
	if !HasOpRef([]byte("X=op://vault/item/field\n")) {
		t.Error("op:// を見つけない")
	}
	if HasOpRef([]byte("Z=literal\n")) {
		t.Error("op:// がないのに true")
	}
}

func TestArgv(t *testing.T) {
	got := Argv("/abs/dev", []string{"/p/.env", "/c/secrets.env"}, []string{"nvim", "."})
	want := []string{"op", "run", "--no-masking",
		"--env-file=/p/.env", "--env-file=/c/secrets.env",
		"--", "/abs/dev", "nvim", "."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Argv =\n%v\nwant\n%v", got, want)
	}
}
