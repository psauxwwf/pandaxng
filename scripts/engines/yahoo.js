return await search({
  rows: "#web .algo, #web li .dd.algo, .searchCenterMiddle .algo",
  links: ["h3 a", "a:has(h3)"], titles: ["h3"],
  snippets: [".compText p", ".compText", ".fz-ms", "p"],
  empty: ["We did not find results for", "No results found"]
});
