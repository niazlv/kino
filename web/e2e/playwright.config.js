// Прогон витрины в режиме телевизора: настоящий браузер, настоящие нажатия.
// Экран как у телевизора — 1920×1080 при плотности 1: именно на такой
// комбинации витрина включает свой масштаб раскладки.
export default {
  testDir: '.',
  timeout: 45000,
  expect: { timeout: 10000 },
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:5173',
    viewport: { width: 1920, height: 1080 },
    deviceScaleFactor: 1,
    launchOptions: {
      args: ['--autoplay-policy=no-user-gesture-required', '--mute-audio'],
    },
  },
}
