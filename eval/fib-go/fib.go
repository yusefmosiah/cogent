package main

func Fibonacci(n int) []int {
	if n <= 0 {
		return []int{}
	}
	if n == 1 {
		return []int{0}
	}
	result := make([]int, n)
	result[0] = 0
	result[1] = 1
	for i := 2; i < n; i++ {
		result[i] = result[i-1] + result[i-2]
	}
	return result
}
