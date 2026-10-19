return await search({
  rows: "#links .web-result, .results .result",
  links: ["a.result__a"], titles: [".result__title", "a.result__a"],
  snippets: [".result__snippet"], ads: ".result--ad",
  empty: ["No results found for", "No more results found"]
});
