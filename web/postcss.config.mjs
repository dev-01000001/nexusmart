const config = {
  plugins: {
    "@tailwindcss/postcss": {},
    // Next drops its built-in autoprefixer once a PostCSS config exists; keep legacy CSS output identical.
    // Targets = Next's default MODERN_BROWSERSLIST_TARGET. Remove in phase 5 with the CSS Modules.
    autoprefixer: {
      flexbox: "no-2009",
      overrideBrowserslist: ["chrome 64", "edge 79", "firefox 67", "opera 51", "safari 12"],
    },
  },
};

export default config;
