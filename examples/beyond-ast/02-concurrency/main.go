package concurrency

import (
	"fmt"
	"sync"
)

// Dispatcher runs workers and sends tasks through a queue.
func Dispatcher(tasks []string) {
	workQueue := make(chan string, 10)
	var wg sync.WaitGroup

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go worker(i, workQueue, &wg)
	}

	for _, task := range tasks {
		workQueue <- task
	}
	close(workQueue)
	wg.Wait()
}

func worker(id int, queue <-chan string, wg *sync.WaitGroup) {
	defer wg.Done()
	for task := range queue {
		fmt.Printf("Worker %d processed task: %s\n", id, task)
	}
}
