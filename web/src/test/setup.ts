import '@testing-library/jest-dom';

// Global mocks for testing environment
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});

// Mock clipboard
Object.defineProperty(navigator, 'clipboard', {
  value: {
    writeText: async () => {},
  },
  writable: true,
});

// The app picks its default language from the browser; keep tests on Chinese.
Object.defineProperty(navigator, 'languages', { value: ['zh-CN'], configurable: true });
Object.defineProperty(navigator, 'language', { value: 'zh-CN', configurable: true });
