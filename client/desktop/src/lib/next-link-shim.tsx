import { forwardRef, type AnchorHTMLAttributes } from "react";

const NextLinkShim = forwardRef<HTMLAnchorElement, AnchorHTMLAttributes<HTMLAnchorElement>>(
  function NextLinkShim({ href, ...props }, ref) {
    return <a ref={ref} href={href} {...props} />;
  },
);

export default NextLinkShim;
