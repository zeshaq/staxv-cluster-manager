/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,jsx}'],
  theme: {
    extend: {
      colors: {
        // Dark canvas — near-black with a teal tint. Deeper than plain
        // slate-900 so the teal accents pop. Lower numbers = darker.
        canvas: {
          900: '#050e14',   // page bg
          800: '#0a1820',   // card bg
          700: '#0f252f',   // elevated card
          600: '#153340',   // hover surface
          500: '#1e4451',   // border strong
          400: '#2a5a68',   // border stronger / input
        },
        // Brand teal — primary interactive + accents. 400 is the
        // "StaxV" wordmark color; 500 is the default button; 600 hover.
        brand: {
          50:  '#effffc',
          100: '#c7fff5',
          200: '#8ff5e3',
          300: '#51e6d0',
          400: '#22d3bb',   // primary (wordmark, active tab, icon)
          500: '#0fb9a4',   // button bg
          600: '#0e9589',   // button hover
          700: '#117772',
          800: '#135d5b',
          900: '#134e4b',
        },
      },
      fontFamily: {
        sans: ['"Inter"', '-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', 'Roboto', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"Fira Code"', 'Menlo', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
