# Day 5: сравнение моделей через API

## Задача

# Задача для сравнения моделей

Реши алгоритмическую задачу и дай ответ как для контеста.

Нужно написать решение на Go.

## Условие

Дан неориентированный взвешенный граф с `n` вершинами и `m` ребрами. Вес каждого ребра неотрицательный.

Нужно найти длину кратчайшего пути из вершины `1` во все остальные вершины.

Если вершина недостижима из `1`, выведите `-1`.

## Входные данные

Первая строка содержит:

```text
n m
```

Следующие `m` строк содержат:

```text
a b w
```

Это означает ребро между вершинами `a` и `b` с весом `w`.

Ограничения:

```text
1 <= n <= 2 * 10^5
0 <= m <= 2 * 10^5
1 <= a, b <= n
0 <= w <= 10^9
```

## Выходные данные

Выведите `n` чисел: расстояния от вершины `1` до вершин `1..n`, для недостижимых вершин выведите `-1`.

## Что нужно в ответе

1. Кратко объясни алгоритм.
2. Обоснуй корректность.
3. Оцени сложность.
4. Дай полный код на Go.
5. Отдельно упомяни, почему нужен `int64`.

## Источники и модели

- Hugging Face Open LLM Leaderboard использовался как ориентир для подбора моделей.
- DeepSeek API: `https://api-docs.deepseek.com/` и `https://api-docs.deepseek.com/quick_start/pricing`.
- Groq models/pricing: `https://console.groq.com/docs/models`.

- `GPT OSS 20B` (Groq, `openai/gpt-oss-20b`).
- `DeepSeek V4 Flash` (DeepSeek, `deepseek-v4-flash`).
- `DeepSeek V4 Pro` (DeepSeek, `deepseek-v4-pro`).

## Настройки

- temperature: `0.20`
- max_tokens: `4096`
- task_name: `model_comparison`
- DeepSeek thinking: `disabled`, чтобы сравнение было ближе к обычному chat-completion режиму.

## Метрики

| Провайдер | Модель | Время | Input | Output | Total | Tokens/sec | Цена input/output за 1M | Стоимость |
|---|---|---:|---:|---:|---:|---:|---|---:|
| Groq | `openai/gpt-oss-20b` | 2.10 s | 419 | 1573 | 1992 | 749.5 | $0.075 miss / $0.300 out (standard) | $0.00050332 |
| DeepSeek | `deepseek-v4-flash` | 526 ms | 361 | 916 | 1277 | 1739.8 | $0.007 hit, $0.220 miss / $0.660 out (off-peak) | $0.00062945 |
| DeepSeek | `deepseek-v4-pro` | 402 ms | 361 | 1136 | 1497 | 2822.6 | $0.022 hit, $0.660 miss / $1.980 out (off-peak) | $0.00232421 |

## Ответ: GPT OSS 20B

_Provider: Groq; model: `openai/gpt-oss-20b`; time=2.10 s; tokens: prompt=419, completion=1573, total=1992; estimated_cost=$0.00050332; finish_reason=stop_

**Алгоритм**

1. Построить список смежности: для каждой вершины хранить пары `(сосед, вес)`.
2. Запустить алгоритм Дейкстры от вершины `1`  
   * `dist[i]` – минимальная известная длина пути от `1` до `i` (тип `int64`).  
   * Инициализировать все `dist` как `INF` (`1<<63-1`), `dist[1]=0`.  
   * Вставить в приоритетную очередь вершину `1` со значением `0`.  
   * Пока очередь не пуста:  
     * Вытаскиваем вершину `v` с минимальным расстоянием `d`.  
     * Если `d` больше текущего `dist[v]` – это устаревшая запись, пропускаем.  
     * Для каждого ребра `(v, u, w)` проверяем, не улучшится ли путь:  
       `if dist[u] > d + w` → обновляем `dist[u]` и помещаем `(u, dist[u])` в очередь.
3. После завершения:  
   * Если `dist[i] == INF` → вершина недостижима → выводим `-1`.  
   * Иначе выводим `dist[i]`.

**Корректность**

Алгоритм Дейкстры работает для графов с неотрицательными весами.  
Пусть в момент извлечения вершины `v` из очереди её расстояние `d` уже является минимальным.  
Поскольку все веса неотрицательные, любой путь, проходящий через `v`, будет иметь длину ≥ `d`.  
Следовательно, когда мы обновляем соседей, мы гарантируем, что получаем действительно минимальные расстояния.  
Постоянное сравнение `d > dist[v]` отбрасывает устаревшие записи, поэтому каждая вершина обрабатывается только с минимальным расстоянием.  
Таким образом, после завершения все `dist[i]` содержат истинные кратчайшие расстояния, а `INF` означает недостижимость.

**Сложность**

* Построение списка смежности: `O(n + m)`.  
* Алгоритм Дейкстры с бинарной кучей: каждая вершина может попасть в очередь несколько раз, но общее число извлечений ≤ `m`.  
  Время работы: `O((n + m) log n)`.  
* Память: `O(n + m)` для списка смежности и очереди.

**Почему нужен `int64`**

* Максимальная длина пути может быть до `m * w_max = 2·10^5 · 10^9 = 2·10^14`, что превышает диапазон `int32`.  
* При сложении двух весов (`d + w`) возможен переполнение `int32`.  
* Поэтому все расстояния и веса храним в `int64`.

---

## Код (Go 1.17+)

```go
package main

import (
	"bufio"
	"container/heap"
	"fmt"
	"os"
)

type Edge struct {
	to   int
	w    int64
}

type Item struct {
	v int
	d int64
}

type PriorityQueue []Item

func (pq PriorityQueue) Len() int { return len(pq) }
func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].d < pq[j].d
}
func (pq PriorityQueue) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }

func (pq *PriorityQueue) Push(x interface{}) {
	*pq = append(*pq, x.(Item))
}
func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	it := old[n-1]
	*pq = old[:n-1]
	return it
}

func main() {
	in := bufio.NewReader(os.Stdin)
	var n, m int
	if _, err := fmt.Fscan(in, &n, &m); err != nil {
		return
	}

	graph := make([][]Edge, n+1)
	for i := 0; i < m; i++ {
		var a, b int
		var w int64
		fmt.Fscan(in, &a, &b, &w)
		graph[a] = append(graph[a], Edge{to: b, w: w})
		graph[b] = append(graph[b], Edge{to: a, w: w})
	}

	const INF int64 = 1<<63 - 1
	dist := make([]int64, n+1)
	for i := 1; i <= n; i++ {
		dist[i] = INF
	}
	dist[1] = 0

	pq := &PriorityQueue{}
	heap.Init(pq)
	heap.Push(pq, Item{v: 1, d: 0})

	for pq.Len() > 0 {
		it := heap.Pop(pq).(Item)
		v, d := it.v, it.d
		if d != dist[v] { // устаревшая запись
			continue
		}
		for _, e := range graph[v] {
			if dist[e.to] > d+e.w {
				dist[e.to] = d + e.w
				heap.Push(pq, Item{v: e.to, d: dist[e.to]})
			}
		}
	}

	out := bufio.NewWriter(os.Stdout)
	for i := 1; i <= n; i++ {
		if dist[i] == INF {
			fmt.Fprint(out, -1)
		} else {
			fmt.Fprint(out, dist[i])
		}
		if i < n {
			fmt.Fprint(out, " ")
		}
	}
	fmt.Fprintln(out)
	out.Flush()
}
```

**Пояснение к коду**

* `Edge` – структура ребра с целевой вершиной и весом (`int64`).  
* `PriorityQueue` реализует `container/heap` и хранит пары `(вершина, расстояние)`.  
* В цикле Дейкстры проверяем, что извлечённое расстояние совпадает с текущим `dist[v]`; иначе это устаревшая запись.  
* После завершения выводим расстояния, заменяя `INF` на `-1`.  

Код полностью соответствует требованиям задачи и использует `int64` для безопасных вычислений.

## Ответ: DeepSeek V4 Flash

_Provider: DeepSeek; model: `deepseek-v4-flash`; time=526 ms; tokens: prompt=361, completion=916, total=1277, cache_hit=256, cache_miss=105; estimated_cost=$0.00062945; finish_reason=stop_

## Решение

### 1. Алгоритм
Используем алгоритм Дейкстры с приоритетной очередью (min-heap). Так как веса неотрицательные, Дейкстра гарантированно находит кратчайшие пути.

### 2. Корректность
Доказательство корректности алгоритма Дейкстры:
- Инвариант: когда вершина извлекается из очереди, её расстояние уже минимально.
- Так как все веса неотрицательные, не существует пути короче через ещё не обработанные вершины.
- При извлечении вершины с минимальным расстоянием, все возможные улучшения через другие вершины уже рассмотрены.

### 3. Сложность
- Время: O((n + m) log n) — каждая вершина извлекается один раз, каждое ребро рассматривается один раз, операции с heap занимают O(log n).
- Память: O(n + m) — хранение графа и расстояний.

### 4. Код на Go

```go
package main

import (
    "container/heap"
    "fmt"
)

type Edge struct {
    to   int
    cost int64
}

type Item struct {
    node int
    dist int64
}

type PriorityQueue []*Item

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
    return pq[i].dist < pq[j].dist
}

func (pq PriorityQueue) Swap(i, j int) {
    pq[i], pq[j] = pq[j], pq[i]
}

func (pq *PriorityQueue) Push(x interface{}) {
    *pq = append(*pq, x.(*Item))
}

func (pq *PriorityQueue) Pop() interface{} {
    old := *pq
    n := len(old)
    item := old[n-1]
    *pq = old[0 : n-1]
    return item
}

func main() {
    var n, m int
    fmt.Scan(&n, &m)

    graph := make([][]Edge, n+1)
    for i := 0; i < m; i++ {
        var a, b int
        var w int64
        fmt.Scan(&a, &b, &w)
        graph[a] = append(graph[a], Edge{b, w})
        graph[b] = append(graph[b], Edge{a, w})
    }

    const INF int64 = 1e18
    dist := make([]int64, n+1)
    for i := range dist {
        dist[i] = INF
    }
    dist[1] = 0

    pq := &PriorityQueue{}
    heap.Init(pq)
    heap.Push(pq, &Item{node: 1, dist: 0})

    for pq.Len() > 0 {
        item := heap.Pop(pq).(*Item)
        u := item.node
        d := item.dist

        if d > dist[u] {
            continue
        }

        for _, edge := range graph[u] {
            v := edge.to
            nd := d + edge.cost
            if nd < dist[v] {
                dist[v] = nd
                heap.Push(pq, &Item{node: v, dist: nd})
            }
        }
    }

    for i := 1; i <= n; i++ {
        if dist[i] == INF {
            fmt.Print("-1 ")
        } else {
            fmt.Print(dist[i], " ")
        }
    }
    fmt.Println()
}
```

### 5. Почему нужен `int64`

Максимальный вес ребра: 10^9. Максимальное количество рёбер в пути: n-1 ≤ 2*10^5 - 1.

Максимальное возможное расстояние: (2*10^5 - 1) * 10^9 ≈ 2*10^14.

Это значение превышает максимальное значение `int32` (≈2.1*10^9), поэтому для хранения расстояний и весов необходимо использовать `int64`, который поддерживает значения до ~9.2*10^18.

## Ответ: DeepSeek V4 Pro

_Provider: DeepSeek; model: `deepseek-v4-pro`; time=402 ms; tokens: prompt=361, completion=1136, total=1497, cache_hit=256, cache_miss=105; estimated_cost=$0.00232421; finish_reason=stop_

Для решения задачи используем алгоритм Дейкстры с приоритетной очередью (min-heap).

## Алгоритм

1. Строим список смежности для графа.
2. Инициализируем массив расстояний `dist` значением `math.MaxInt64`.
3. Устанавливаем `dist[0] = 0` (вершина 1).
4. Используем min-heap для хранения пар `(расстояние, вершина)`.
5. На каждом шаге извлекаем вершину с минимальным расстоянием, если она уже обработана — пропускаем.
6. Для каждого соседа пытаемся улучшить расстояние, если нашли лучшее — обновляем и добавляем в кучу.
7. Выводим результат: для недостижимых вершин выводим `-1`.

## Корректность

Алгоритм Дейкстры корректен для графов с неотрицательными весами. На каждом шаге мы выбираем вершину с минимальным известным расстоянием, которое уже не может быть улучшено, так как все рёбра неотрицательные. Это гарантирует, что когда вершина извлекается из кучи, её расстояние окончательное.

## Сложность

- **Время**: O((n + m) log n) — каждая вершина и ребро обрабатываются не более одного раза, операции с кучей занимают O(log n).
- **Память**: O(n + m) — для хранения графа и расстояний.

## Почему нужен int64

Вес ребра может достигать 10^9, а количество рёбер — 2 * 10^5. Максимальная длина пути может быть до 2 * 10^14, что превышает диапазон int32 (до ~2.1 * 10^9). Поэтому используем int64 для хранения расстояний.

## Код на Go

```go
package main

import (
	"bufio"
	"container/heap"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Edge struct {
	to     int
	weight int64
}

type Item struct {
	dist int64
	node int
}

type PriorityQueue []Item

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].dist < pq[j].dist
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
}

func (pq *PriorityQueue) Push(x interface{}) {
	*pq = append(*pq, x.(Item))
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	*pq = old[0 : n-1]
	return item
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	line := scanner.Text()
	parts := strings.Fields(line)
	n, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])

	graph := make([][]Edge, n)
	for i := 0; i < m; i++ {
		scanner.Scan()
		line = scanner.Text()
		parts = strings.Fields(line)
		a, _ := strconv.Atoi(parts[0])
		b, _ := strconv.Atoi(parts[1])
		w, _ := strconv.ParseInt(parts[2], 10, 64)
		a--
		b--
		graph[a] = append(graph[a], Edge{to: b, weight: w})
		graph[b] = append(graph[b], Edge{to: a, weight: w})
	}

	const INF = int64(1) << 62
	dist := make([]int64, n)
	for i := range dist {
		dist[i] = INF
	}
	dist[0] = 0

	pq := &PriorityQueue{{dist: 0, node: 0}}
	heap.Init(pq)

	for pq.Len() > 0 {
		item := heap.Pop(pq).(Item)
		d := item.dist
		u := item.node
		if d > dist[u] {
			continue
		}
		for _, edge := range graph[u] {
			v := edge.to
			w := edge.weight
			if dist[u]+w < dist[v] {
				dist[v] = dist[u] + w
				heap.Push(pq, Item{dist: dist[v], node: v})
			}
		}
	}

	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()
	for i := 0; i < n; i++ {
		if dist[i] == INF {
			fmt.Fprint(writer, "-1")
		} else {
			fmt.Fprint(writer, dist[i])
		}
		if i != n-1 {
			fmt.Fprint(writer, " ")
		}
	}
	fmt.Fprintln(writer)
}
```

## Краткий вывод

- Самая быстрая по wall-clock времени: `deepseek-v4-pro` (402 ms).
- Самая дешевая в этом запуске: `openai/gpt-oss-20b` ($0.00050332).
- Меньше всего токенов потратила: `deepseek-v4-flash` (1277 total tokens).
- Качество лучше смотреть по разделу API-оценки и по самим ответам: метрики скорости и цены не гарантируют правильность решения.
