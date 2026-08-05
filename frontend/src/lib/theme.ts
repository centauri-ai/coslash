export type Theme = 'light' | 'dark';

export function getTheme(value: string | null): Theme {
  return value === 'dark' ? 'dark' : 'light';
}

export function setTheme(theme: Theme): void {
  document.documentElement.classList.toggle('dark', theme === 'dark');
  localStorage.setItem('theme', theme);
}
