import { defineConfig } from 'vitest/config'

// Vitest 4 больше не исключает dist/ по умолчанию, а `pnpm build` (tsc)
// кладёт туда скомпилированные копии тестов — без include каждый тест
// гонялся бы дважды, второй раз по устаревшей сборке.
export default defineConfig({
  test: { include: ['src/**/*.test.ts'] },
})
