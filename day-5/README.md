# Day 5

Сравнение трех моделей через API:

- `openai/gpt-oss-20b` через Groq;
- `deepseek-v4-flash` через DeepSeek;
- `deepseek-v4-pro` через DeepSeek.

Запуск основного промпта:

```sh
./day-5/run.sh
```

Запуск любого промпта из файла:

```sh
./day-5/run_task.sh day-5/task_blue_eyes.md
./day-5/run_task.sh day-5/task_seventh_letter.md
./day-5/run_task.sh day-5/task_equation.md
./day-5/run_task.sh day-5/task_seven_sisters.md
```

Для задачи про 7 сестер есть отдельный короткий запуск:

```sh
./day-5/run_seven_sisters.sh
```

Ключи читаются из `.env`: `DEEPSEEK_API_KEY` и `GROQ_API_KEY`.

Отчет содержит только ответы трех моделей и технические метрики. Корректность ответов оценивается вручную.
