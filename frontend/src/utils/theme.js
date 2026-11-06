// Theme preference is stored locally; the whole app switches by setting
// data-theme on <html> (see styles/theme.scss).
const STORAGE_KEY = 'lops-theme'
const THEMES = ['dark', 'light']

export function getTheme() {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (THEMES.includes(saved)) return saved
  } catch (e) {
    /* private mode / blocked storage */
  }
  return 'dark'
}

export function applyTheme(theme) {
  const next = THEMES.includes(theme) ? theme : 'dark'
  document.documentElement.setAttribute('data-theme', next)
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch (e) {
    /* ignore */
  }
  return next
}
