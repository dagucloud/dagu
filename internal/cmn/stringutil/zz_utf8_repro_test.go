package stringutil

import "testing"

func TestParseSeparatedValuesUTF8(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`"北京 上海" "广州"`, []string{"北京 上海", "广州"}},
		{`foo 你 "bar baz"`, []string{"foo", "你", "bar baz"}},
		{`"café résumé"`, []string{"café résumé"}},
	} {
		got, err := ParseSeparatedValues(tc.in)
		if err != nil {
			t.Fatalf("input %q: %v", tc.in, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("input %q: got %q, want %q", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("input %q item %d: got %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
