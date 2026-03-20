package main

import (
	"reflect"
	"testing"
)

func TestFibonacci(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want []int
	}{
		{"N=0", 0, []int{}},
		{"N=1", 1, []int{0}},
		{"N=10", 10, []int{0, 1, 1, 2, 3, 5, 8, 13, 21, 34}},
		{"N=20", 20, []int{0, 1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 233, 377, 610, 987, 1597, 2584, 4181}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Fibonacci(tt.n)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Fibonacci(%d) = %v, want %v", tt.n, got, tt.want)
			}
		})
	}
}
