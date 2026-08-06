export type Theme = 'light' | 'dark';

export function setTheme(theme: Theme): void {
  document.documentElement.classList.toggle('dark', theme === 'dark');
}
