module.exports = {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: {
    colors: { surface: "var(--c-surface)", subtle: "var(--c-subtle)", action: { DEFAULT: "var(--c-action)", fg: "var(--c-action-fg)", hover: "var(--c-action-hover)", text: "var(--c-action)" },
      "fg-1": "var(--c-fg-1)", "fg-2": "var(--c-fg-2)", "fg-3": "var(--c-fg-3)", border: { DEFAULT: "var(--c-border)", strong: "var(--c-border-strong)" },
      overlay: "rgba(10,10,10,.3)", danger: "#b91c1c", ring: "var(--c-fg-2)" },
    borderRadius: { control: "8px", panel: "12px", dialog: "16px" },
    fontSize: { body: "14px", title: "20px", caption: "12px" },
    boxShadow: { "v7-lg": "0 20px 60px #0002" },
  } },
  plugins: [require("tailwindcss-animate")],
};
